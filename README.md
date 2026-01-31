# Cordum Claude CLI Configuration

This directory contains optimized configuration files for using Claude CLI (Claude Code) with the Cordum codebase.

## Quick Setup

1. **Copy files to your Cordum repository:**

```bash
# Clone or navigate to cordum
cd ~/cordum  # or wherever your cordum repo is

# Copy the configuration files
cp -r /path/to/cordum-claude-cli/* .
cp -r /path/to/cordum-claude-cli/.* . 2>/dev/null || true
```

2. **Set up environment variables for MCP servers:**

```bash
export GITHUB_TOKEN=your_github_token
export BRAVE_API_KEY=your_brave_api_key  # optional, for web search
```

3. **Start using Claude CLI:**

```bash
# Navigate to your cordum directory
cd ~/cordum

# Start Claude CLI
claude

# Or with a specific task
claude "help me understand the Safety Kernel implementation"
```

## File Structure

```
cordum-claude-cli/
├── CLAUDE.md              # Main project context (root level)
├── core/
│   └── CLAUDE.md          # Core libraries context
├── dashboard/
│   └── CLAUDE.md          # React dashboard context
├── sdk/
│   └── CLAUDE.md          # SDK/worker development context
├── .claude/
│   └── settings.json      # Claude CLI settings
├── .mcp.json              # MCP server configurations
├── .claudeignore          # Files to exclude from context
├── COMMANDS.md            # Quick command reference
├── TESTING.md             # Testing guide and patterns
└── README.md              # This file
```

## Configuration Files Explained

### CLAUDE.md (Root)
The main context file that Claude CLI loads. Contains:
- Project architecture overview
- Directory structure
- Coding standards and patterns
- Build/test commands
- Environment variables
- Key design decisions

### core/CLAUDE.md
Specific context for the `core/` directory:
- Safety Kernel implementation details
- Workflow Engine patterns
- Scheduler logic
- Protocol definitions
- Testing requirements for each component

### dashboard/CLAUDE.md
Context for the React dashboard:
- Component patterns
- API integration
- State management
- TypeScript types
- Styling guidelines

### sdk/CLAUDE.md
Context for worker/SDK development:
- Worker lifecycle
- Job context API
- Gateway client usage
- MCP integration patterns
- Error handling

### .claude/settings.json
Claude CLI configuration:
- Model selection (claude-sonnet-4)
- Permission rules (what Claude can read/write)
- Custom instructions
- Context paths to auto-load
- Paths to ignore

### .mcp.json
MCP (Model Context Protocol) server configurations:
- **filesystem**: File system access
- **github**: GitHub API for repository operations
- **memory**: Persistent conversation context
- **sequential-thinking**: Enhanced reasoning
- **brave-search**: Web search (optional)
- **fetch**: HTTP requests for testing

### COMMANDS.md
Quick reference for common operations:
- Development lifecycle commands
- Testing commands
- Redis/NATS operations
- Debugging tools
- Useful aliases

### TESTING.md
Comprehensive testing guide:
- Test categories (unit, integration, contract, benchmark)
- Test infrastructure setup
- Mock generation
- CI pipeline configuration
- Best practices for safety-critical code

## MCP Servers

The `.mcp.json` configures several MCP servers for enhanced capabilities:

| Server | Purpose | Required |
|--------|---------|----------|
| filesystem | Read/write project files | Yes |
| github | GitHub API (PRs, issues) | Recommended |
| memory | Persistent context | Recommended |
| sequential-thinking | Complex reasoning | Optional |
| brave-search | Web search | Optional |
| fetch | HTTP requests | Optional |

## Permissions

The settings define what Claude CLI can access:

**Allowed:**
- Read all files
- Write to source directories (core/, cmd/, sdk/, dashboard/, etc.)
- Run make, go, docker, git commands
- Run Redis and NATS CLI tools

**Denied:**
- Write to .env files
- Access secrets directories
- Destructive shell commands (rm -rf /)
- Sudo commands

## Custom Instructions

Claude is configured with Cordum-specific guidance:
1. Safety-first - Never bypass Safety Kernel
2. Performance - Keep policy evaluation under 5ms
3. Protocol compliance - Follow CAP v2 spec
4. Idempotency - All operations retryable
5. Context propagation - Always pass context.Context
6. Testing - Write table-driven tests
7. Logging - Use structured slog logging

## Usage Examples

### Ask about architecture
```
claude "explain how the Safety Kernel enforces policies"
```

### Get help with code
```
claude "help me add a new policy rule type for rate limiting"
```

### Run tests
```
claude "run the safety kernel tests and explain any failures"
```

### Review changes
```
claude "review my changes to core/scheduler/router.go"
```

### Debug issues
```
claude "jobs are stuck in pending state, help me debug"
```

## Updating Configuration

When the codebase evolves, update the CLAUDE.md files:

1. Add new directories to the root CLAUDE.md structure
2. Create component-specific CLAUDE.md files as needed
3. Update .claudeignore for new generated paths
4. Update COMMANDS.md with new operational commands

## Troubleshooting

### Claude not finding context
- Ensure CLAUDE.md is in the root directory
- Check .claudeignore isn't excluding important files
- Verify contextPaths in settings.json

### MCP servers not working
- Check environment variables are set
- Verify npx is available
- Check network connectivity for remote servers

### Permission denied errors
- Review .claude/settings.json permissions
- Check file ownership in the repository

---

*Generated for Cordum AI Agent Governance Platform*
