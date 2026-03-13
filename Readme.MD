# 🐚 MCP Shell Server (Go Implementation)

[![Build and Release Binaries](https://github.com/mrqzzz/shell-mcp/actions/workflows/release.yml/badge.svg)](https://github.com/mrqzzz/shell-mcp/actions/workflows/release.yml)

## 📥 Download Latest Release

| Platform | Architecture | Download |
|----------|-------------|----------|
| Linux | x86-64 (amd64) | [shell-mcp-linux-amd64](https://github.com/mrqzzz/shell-mcp/releases/latest/download/shell-mcp-linux-amd64) |
| Linux | ARM64 | [shell-mcp-linux-arm64](https://github.com/mrqzzz/shell-mcp/releases/latest/download/shell-mcp-linux-arm64) |
| macOS | Intel (x86-64) | [shell-mcp-macos-intel](https://github.com/mrqzzz/shell-mcp/releases/latest/download/shell-mcp-macos-intel) |
| macOS | Apple Silicon (ARM64) | [shell-mcp-macos-silicon](https://github.com/mrqzzz/shell-mcp/releases/latest/download/shell-mcp-macos-silicon) |
| Windows | x86-64 (amd64) | [shell-mcp-windows-amd64.exe](https://github.com/mrqzzz/shell-mcp/releases/latest/download/shell-mcp-windows-amd64.exe) |
| Windows | ARM64 | [shell-mcp-windows-arm64.exe](https://github.com/mrqzzz/shell-mcp/releases/latest/download/shell-mcp-windows-arm64.exe) |

A Go-based **MCP (Model Context Protocol)** server that provides AI agents (like Claude Desktop or Claude Code) with **real, persistent, and interactive** access to your system's terminal.

Unlike other tools that execute isolated commands (e.g., via `subprocess` or standard `exec`), this server utilizes **PTY (Pseudo-Terminal)**. This means the session stays alive between requests, allowing for complex scenarios such as SSH connections, directory navigation, and handling interactive prompts (like entering passwords).


This MCP-server works well with **agent-go** https://github.com/mrqzzz/agent-go


## 🚀 Key Features

* **Persistent Session (Stateful):** If you execute `cd /var/www`, the next command will be executed in that specific directory. Environment variables (like `export VAR=value`) are retained across calls.
* **Interactive Support:** Correctly handles programs that require user input, such as `ssh`, `sudo`, or interactive Python scripts. The AI can read a "Password:" prompt, send the password, and proceed seamlessly.
* **True Cross-Platform:**
    * **macOS / Linux:** Utilizes native Unix PTYs.
    * **Windows:** Natively utilizes the **ConPTY** (Console PseudoTerminal) APIs for flawless PowerShell support.
* **Highly Portable:** Being written in Go, it compiles into a single standalone executable file with no external dependencies (no need for Python virtual environments or giant `node_modules` folders).

## 🛠️ Prerequisites

* [Go](https://go.dev/dl/) installed (version 1.21+ recommended).

## 📦 Installation and Build

1. **Initialize the project (if you haven't already):**
   ```bash
   mkdir shell-mcp
   cd shell-mcp
   go mod init shell-mcp
   ```

2. **Download the required dependencies:**
   ```bash
   go get [github.com/creack/pty](https://github.com/creack/pty)
   go get [github.com/mark3labs/mcp-go](https://github.com/mark3labs/mcp-go)
   ```

3. **Build the source code:**
   ```bash
   go build -o shell-mcp main.go
   ```
   *(This will create an executable named `shell-mcp`, or `shell-mcp.exe` on Windows).*

## ⚙️ Configuration with Claude Desktop

To use the server with Claude Desktop, you need to update its configuration file.

### Location of `claude_desktop_config.json`
* **macOS:** `~/Library/Application Support/Claude/claude_desktop_config.json`
* **Windows:** `%APPDATA%\Claude\claude_desktop_config.json`

### Configuration Snippet

Add the `shell-mcp` entry to your `mcpServers` list:

```json
{
  "mcpServers": {
    "shell-mcp": {
      "command": "/ABSOLUTE/PATH/TO/YOUR/PROJECT/shell-mcp",
      "args": []
    }
  }
}
```

> **Note:** Make sure to use the full absolute path to the compiled binary. On Windows, remember to escape backslashes (e.g., `C:\\Users\\Name\\shell-mcp.exe`).

## 📖 Usage and Available Tools

The AI will have access to three primary tools:

### 1. `terminal_write`
Executes a command or sends input to the active shell.
* **Arguments:** `command` (string).
* **Examples:**
    * AI: `terminal_write("ls -la")` -> Returns the file list.
    * AI: `terminal_write("ssh user@server")` -> Returns the "Password:" prompt.
    * AI: `terminal_write("mypassword")` -> Logs in.

### 2. `terminal_read`
Reads the current output buffer without sending any new commands.
* **Usage:** Very useful if a previously executed command is taking a long time, or if the AI wants to check for new logs/asynchronous output without interrupting the process.

### 3. `terminal_reset`
Resets the terminal by terminating the current shell session and starting a fresh one.
* **Usage:** Use when the terminal is in a broken or unrecoverable state (e.g., stuck process, corrupted session).
* **Warning:** This destroys all state in the current session:
    * The current working directory is reset to the default.
    * All environment variables set during the session are lost.
    * Any running background processes started in the shell are terminated.
    * All command history and buffered output are cleared.

## 💡 Example: SSH Scenario

Here is how the AI can interact using this server:

1. **User:** "Check the disk space on the remote server 192.168.1.50"
2. **Claude (Tool Call):** `terminal_write("ssh admin@192.168.1.50")`
3. **MCP Server:** Returns `admin@192.168.1.50's password:`
4. **Claude (Tool Call):** `terminal_write("SecretPassword123")`
5. **MCP Server:** Returns `Welcome to Ubuntu... admin@server:~$`
6. **Claude (Tool Call):** `terminal_write("df -h")`
7. **MCP Server:** Returns the standard output of the `df` command.
8. **Claude:** "Here is the disk space of the remote server: ..."

## ⚠️ Security Warning

**WARNING:** This tool provides the AI (and the underlying LLM provider) with **complete access to your computer's terminal** using your current user's privileges.

* The AI can read, modify, and delete files.
* The AI can install packages or execute arbitrary scripts.
* **Recommendation:** Use this server with caution. For maximum security, run it inside a Virtual Machine, a Docker container, or under a restricted user account.

## 🐛 Troubleshooting

* **`request.Params.Arguments` Error:** Ensure you have compiled the latest version of the code that includes the fix for the `(map[string]interface{})` type assertion.
* **Unresponsive Interactive Shell:** If some commands seem to hang, try prompting the AI to use `terminal_read` to see if the terminal is waiting for a hidden input prompt.
* **Windows ANSI Characters:** If you see strange characters on Windows, it's because the server outputs ANSI color sequences. Usually, Claude parses them fine, but the raw output might look messy in debug logs.