package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/creack/pty"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// ShellSession maintains the state of the active shell
type ShellSession struct {
	ptmx   *os.File     // The file descriptor of the pseudo-terminal
	cmd    *exec.Cmd    // The running command
	mu     sync.Mutex   // Protects the start/stop state
	bufMu  sync.Mutex   // Protects exclusively the output buffer
	buffer bytes.Buffer // Buffer that accumulates asynchronous output
	active bool
}

var session = &ShellSession{active: false}

// startSession initializes the shell based on the operating system
func (s *ShellSession) startSession() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.active && s.cmd != nil && s.cmd.ProcessState == nil {
		return nil // Already active
	}

	var shellCmd string
	var args []string

	// OS detection
	if runtime.GOOS == "windows" {
		shellCmd = "powershell.exe"
	} else {
		// Mac and Linux: use the system shell
		shell := os.Getenv("SHELL")
		if shell == "" {
			shellCmd = "/bin/bash"
			if runtime.GOOS == "darwin" {
				shellCmd = "/bin/zsh" // Default on modern Macs
			}
		} else {
			shellCmd = shell
		}
	}

	c := exec.Command(shellCmd, args...)
	c.Env = append(os.Environ(), "TERM=xterm-256color")

	// Start the PTY
	f, err := pty.Start(c)
	if err != nil {
		return fmt.Errorf("failed to start shell: %w", err)
	}

	// Set initial terminal size (24 Rows, 80 Columns)
	_ = pty.Setsize(f, &pty.Winsize{Rows: 24, Cols: 80, X: 0, Y: 0})

	s.ptmx = f
	s.cmd = c
	s.buffer.Reset() // Clear previous buffers
	s.active = true

	// CONTINUOUS READING GOROUTINE
	// This solves the "does not support deadline" error.
	// Constantly reads in the background and safely fills the buffer.
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := s.ptmx.Read(buf)
			if n > 0 {
				s.bufMu.Lock()
				s.buffer.Write(buf[:n])
				s.bufMu.Unlock()
			}
			if err != nil {
				// If there is an error (e.g., EOF because the shell closed)
				s.mu.Lock()
				s.active = false
				s.mu.Unlock()

				s.bufMu.Lock()
				s.buffer.WriteString("\n[Session terminated: " + err.Error() + "]")
				s.bufMu.Unlock()
				break // Exit the loop and terminate the goroutine
			}
		}
	}()

	// Give the shell time to print the initial header/banner
	// and clear the buffer so the first command doesn't see the login output
	go func() {
		time.Sleep(500 * time.Millisecond)
		s.bufMu.Lock()
		s.buffer.Reset()
		s.bufMu.Unlock()
	}()

	return nil
}

// writeInput writes commands to the terminal
func (s *ShellSession) writeInput(command string) (string, error) {
	if !s.active {
		if err := s.startSession(); err != nil {
			return "", err
		}
	}

	isMultiline := strings.Count(command, "\n") > 0

	// Add newline if missing to simulate pressing Enter
	if len(command) == 0 || command[len(command)-1] != '\n' {
		command += "\n"
	}

	// For multiline payloads, send as bracketed paste to reduce shell line-editor corruption.
	if isMultiline {
		trimmed := strings.TrimSuffix(command, "\n")
		command = "\x1b[200~" + trimmed + "\x1b[201~\n"
	}

	// Write to PTY (handle partial writes to avoid truncating large multiline commands)
	payload := []byte(command)
	written := 0
	chunkSize := len(payload)
	chunkDelay := time.Duration(0)
	if isMultiline {
		chunkSize = 256
		chunkDelay = 2 * time.Millisecond
	}

	s.mu.Lock()
	for written < len(payload) {
		end := written + chunkSize
		if end > len(payload) {
			end = len(payload)
		}

		for written < end {
			n, err := s.ptmx.Write(payload[written:end])
			if n > 0 {
				written += n
			}
			if err != nil {
				s.mu.Unlock()
				s.active = false
				return "", fmt.Errorf("shell write error: %w", err)
			}
			if n == 0 {
				s.mu.Unlock()
				s.active = false
				return "", fmt.Errorf("shell write error: %w", io.ErrShortWrite)
			}
		}

		if chunkDelay > 0 {
			time.Sleep(chunkDelay)
		}
	}
	s.mu.Unlock()

	// Wait until terminal output becomes idle so large multiline inputs
	// (e.g. heredocs) are fully consumed before we return control.
	if isMultiline {
		return s.readOutputWhenIdle(12*time.Second, 600*time.Millisecond)
	}
	return s.readOutputWhenIdle(4*time.Second, 300*time.Millisecond)
}

// readOutputWhenIdle collects output until either:
// 1) no new output arrives for idleFor, or
// 2) maxWait is reached.
func (s *ShellSession) readOutputWhenIdle(maxWait time.Duration, idleFor time.Duration) (string, error) {
	if maxWait <= 0 {
		maxWait = 4 * time.Second
	}
	if idleFor <= 0 {
		idleFor = 300 * time.Millisecond
	}

	deadline := time.Now().Add(maxWait)
	lastLen := -1
	lastChange := time.Now()

	for {
		s.bufMu.Lock()
		currentLen := s.buffer.Len()
		s.bufMu.Unlock()

		if currentLen != lastLen {
			lastLen = currentLen
			lastChange = time.Now()
		} else if time.Since(lastChange) >= idleFor {
			break
		}

		if time.Now().After(deadline) {
			break
		}

		time.Sleep(50 * time.Millisecond)
	}

	return s.readOutput()
}

// readOutput "collects" all output currently present in the buffer
func (s *ShellSession) readOutput() (string, error) {
	s.bufMu.Lock()
	defer s.bufMu.Unlock()

	// Get all text
	out := s.buffer.String()
	// Clear the buffer to avoid reading the same things next time
	s.buffer.Reset()

	// If the session is dead and there is no more output, return an error
	if !s.active && out == "" {
		return "", fmt.Errorf("no active session or shell terminated")
	}

	return out, nil
}

func main() {
	s := server.NewMCPServer(
		"GoShellWrapper",
		"1.0.0",
		server.WithLogging(),
	)

	if err := session.startSession(); err != nil {
		fmt.Fprintf(os.Stderr, "Error starting initial shell: %v\n", err)
	}

	// Tool: Write Commands
	toolWrite := mcp.NewTool("terminal_write",
		mcp.WithDescription(
			"Executes a command or sends input to the shell. "+
				"CRITICAL INSTRUCTION FOR AI: This is a STATEFUL and INTERACTIVE shell session (Pseudo-Terminal), NOT a stateless execution environment. "+
				"1. STATE IS PRESERVED: If you change directories ('cd') or set environment variables ('export'), they persist for all subsequent commands. "+
				"2. INTERACTIVE SEQUENCES: You DO NOT need to chain multiple commands in a single call. Send one command, receive the output, reason about it, and then send the next. "+
				"3. HANDLING PROMPTS: If a command requires interaction (e.g., 'ssh', 'sudo', 'python'), send the initial command, wait to receive the prompt in the output (like 'Password:'), and then use this tool again to send the required input. "+
				"You can natively interact with CLI tools like vim, nano, REPLs, etc.",
		),
		mcp.WithString("command", mcp.Required(), mcp.Description("The command to execute or the interactive input to type")),
	)

	s.AddTool(toolWrite, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args, ok := request.Params.Arguments.(map[string]interface{})
		if !ok {
			return mcp.NewToolResultError("Internal error: invalid arguments format"), nil
		}

		cmdRaw, ok := args["command"]
		if !ok {
			return mcp.NewToolResultError("Missing 'command' argument"), nil
		}

		cmd, ok := cmdRaw.(string)
		if !ok {
			return mcp.NewToolResultError("The 'command' argument must be a string"), nil
		}

		output, err := session.writeInput(cmd)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("Error: %v", err)), nil
		}

		return mcp.NewToolResultText(output), nil
	})

	// Tool: Read Output
	toolRead := mcp.NewTool("terminal_read",
		mcp.WithDescription(
			"Reads the current terminal output buffer without sending any new commands. "+
				"Use this tool if you executed a long-running command using 'terminal_write' and you want to poll the terminal to see if new output or logs have been generated.",
		),
	)

	s.AddTool(toolRead, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		// A small delay to allow any asynchronous writes before reading
		time.Sleep(100 * time.Millisecond)
		output, err := session.readOutput()
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("Error: %v", err)), nil
		}
		return mcp.NewToolResultText(output), nil
	})

	// Tool: Reset Terminal
	toolReset := mcp.NewTool("terminal_reset",
		mcp.WithDescription(
			"Resets the terminal by terminating the current shell session and starting a fresh one. "+
				"WARNING: This will destroy all state in the current session, including: "+
				"1. The current working directory will be reset to the default. "+
				"2. All environment variables set during the session will be lost. "+
				"3. Any running background processes started in the shell will be terminated. "+
				"4. All command history and buffered output will be cleared. "+
				"Use this only when the terminal is in a broken or unrecoverable state (e.g., stuck process, corrupted session).",
		),
	)

	s.AddTool(toolReset, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		session.mu.Lock()
		if session.ptmx != nil {
			session.ptmx.Close()
		}
		if session.cmd != nil && session.cmd.Process != nil {
			session.cmd.Process.Kill()
		}
		session.active = false
		session.mu.Unlock()

		session.bufMu.Lock()
		session.buffer.Reset()
		session.bufMu.Unlock()

		if err := session.startSession(); err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("Failed to reset terminal: %v", err)), nil
		}

		return mcp.NewToolResultText("Terminal has been reset. A new shell session is now active."), nil
	})

	if err := server.ServeStdio(s); err != nil {
		fmt.Fprintf(os.Stderr, "Server error: %v\n", err)
	}
}
