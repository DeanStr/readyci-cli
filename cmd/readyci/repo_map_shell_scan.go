package main

import (
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

func executableShellArgs(args []shellWord) []shellWord {
	return executableShellCommandFromArgs(args, "", false).Args
}

type executableShellCommand struct {
	Args       []shellWord
	Dir        string
	DirDynamic bool
	// UVProjectSync reports that a uv run wrapper syncs the project it
	// discovers from Dir into its environment before running Args.
	UVProjectSync bool
}

func executableShellCommandFromCommand(command shellCommand) executableShellCommand {
	return executableShellCommandFromArgs(command.Args, command.Dir, command.DirDynamic)
}

func executableShellCommandFromArgs(args []shellWord, dir string, dirDynamic bool) executableShellCommand {
	uvSync := false
	for len(args) > 0 && !args[0].Dynamic {
		switch normalizeCommandInvocationToken(args[0].Text) {
		case "env":
			next, ok := envWrappedCommandArgs(args[1:], dir, dirDynamic)
			if !ok {
				return executableShellCommand{}
			}
			args = next.Args
			dir = next.Dir
			dirDynamic = next.DirDynamic
		case "command":
			next, ok := commandWrappedCommandArgs(args[1:])
			if !ok {
				return executableShellCommand{}
			}
			args = next
		case "uv":
			next, ok := uvRunWrappedCommandArgs(args[1:], dir, dirDynamic)
			if !ok {
				// Other subcommands and unrecognized run options stay opaque.
				return executableShellCommand{Args: args, Dir: dir, DirDynamic: dirDynamic, UVProjectSync: uvSync}
			}
			args, dir, dirDynamic = next.Args, next.Dir, next.DirDynamic
			uvSync = uvSync || next.UVProjectSync
		default:
			return executableShellCommand{Args: args, Dir: dir, DirDynamic: dirDynamic, UVProjectSync: uvSync}
		}
	}
	return executableShellCommand{Args: args, Dir: dir, DirDynamic: dirDynamic, UVProjectSync: uvSync}
}

// uvRunWrappedCommandArgs reads uv run's options. It recognizes uv's
// documented run options and rejects anything else, including --project, which
// discovers the project somewhere other than the working directory.
func uvRunWrappedCommandArgs(args []shellWord, dir string, dirDynamic bool) (executableShellCommand, bool) {
	if len(args) == 0 || args[0].Dynamic || args[0].Text != "run" {
		return executableShellCommand{}, false
	}
	sync := true
	for i := 1; i < len(args); i++ {
		if args[i].Dynamic {
			return executableShellCommand{}, false
		}
		token := args[i].Text
		if token == "--" {
			i++
		}
		if token == "--" || !strings.HasPrefix(token, "-") {
			if i >= len(args) {
				return executableShellCommand{}, false
			}
			return executableShellCommand{Args: args[i:], Dir: dir, DirDynamic: dirDynamic, UVProjectSync: sync}, true
		}
		name, value, inline := strings.Cut(token, "=")
		kind := uvRunOptionKind(name)
		if kind == "flag" || kind == "module" || kind == "script" {
			if inline {
				return executableShellCommand{}, false
			}
		} else if kind != "" && !inline {
			if i+1 >= len(args) || args[i+1].Dynamic {
				return executableShellCommand{}, false
			}
			i++
			value = args[i].Text
		}
		switch kind {
		case "flag":
			if name == "--no-sync" || name == "--no-project" {
				sync = false
			}
		case "value":
		case "directory":
			if next, ok := resolveMakeDir(dir, value); ok {
				dir = next
			} else {
				dirDynamic = true
			}
		case "module":
			if i+1 >= len(args) {
				return executableShellCommand{}, false
			}
			command := append([]shellWord{{Text: "python"}, {Text: "-m"}}, args[i+1:]...)
			return executableShellCommand{Args: command, Dir: dir, DirDynamic: dirDynamic, UVProjectSync: sync}, true
		case "script":
			// A script runs with its inline metadata, not the project environment.
			if i+1 >= len(args) {
				return executableShellCommand{}, false
			}
			command := append([]shellWord{{Text: "python"}}, args[i+1:]...)
			return executableShellCommand{Args: command, Dir: dir, DirDynamic: dirDynamic}, true
		default:
			return executableShellCommand{}, false
		}
	}
	return executableShellCommand{}, false
}

func uvRunOptionKind(name string) string {
	switch name {
	case "--all-extras", "--no-dev", "--dev", "--only-dev", "--no-default-groups", "--all-groups",
		"--no-editable", "--exact", "--no-env-file", "--isolated", "--active", "--no-active",
		"--no-sync", "--locked", "--frozen", "--all-packages", "--no-project", "--no-index",
		"--no-sources", "-U", "--upgrade", "--reinstall", "--compile-bytecode",
		"--no-build-isolation", "--no-build", "--no-binary", "-n", "--no-cache", "--refresh",
		"--managed-python", "--no-managed-python", "--no-python-downloads", "--quiet", "--verbose",
		"--native-tls", "--offline", "--no-progress", "--no-config", "--preview":
		return "flag"
	case "--extra", "--no-extra", "--group", "--no-group", "--only-group", "--env-file", "--with",
		"--with-editable", "--with-requirements", "--package", "-p", "--python", "--python-platform",
		"--index", "--default-index", "-i", "--index-url", "--extra-index-url", "-f", "--find-links",
		"--index-strategy", "--keyring-provider", "-P", "--upgrade-package", "--resolution",
		"--prerelease", "--fork-strategy", "--exclude-newer", "--exclude-newer-package",
		"--reinstall-package", "--link-mode", "-C", "--config-setting", "--config-settings-package",
		"--no-build-isolation-package", "--no-build-package", "--no-binary-package", "--cache-dir",
		"--refresh-package", "--color", "--allow-insecure-host", "--config-file":
		return "value"
	case "--directory":
		return "directory"
	case "-m", "--module":
		return "module"
	case "-s", "--script":
		return "script"
	}
	if len(name) > 1 && strings.Trim(name[1:], "qv") == "" && name[0] == '-' {
		return "flag" // -q, -vv and similar repeated verbosity flags
	}
	return ""
}

func envWrappedCommandArgs(args []shellWord, dir string, dirDynamic bool) (executableShellCommand, bool) {
	for i := 0; i < len(args); i++ {
		arg := args[i]
		token := arg.Text
		if token == "" {
			continue
		}
		if arg.Dynamic {
			if strings.HasPrefix(token, "-") {
				if _, ok := envInlineDirectoryValue(token); ok {
					dirDynamic = true
					continue
				}
				return executableShellCommand{}, false
			}
			if strings.Contains(token, "=") {
				continue
			}
			return executableShellCommand{}, false
		}
		if token == "--" {
			return executableShellCommand{Args: args[i+1:], Dir: dir, DirDynamic: dirDynamic}, true
		}
		if value, ok := envInlineDirectoryValue(token); ok {
			if nextDir, ok := resolveMakeDir(dir, value); ok {
				dir = nextDir
			} else {
				dirDynamic = true
			}
			continue
		}
		if strings.HasPrefix(token, "-") {
			kind := envOptionValueKind(token)
			if kind != "" {
				if i+1 >= len(args) {
					return executableShellCommand{}, false
				}
				next := args[i+1]
				if kind == "directory" {
					if !next.Dynamic {
						var ok bool
						dir, ok = resolveMakeDir(dir, next.Text)
						if !ok {
							dirDynamic = true
						}
					} else {
						dirDynamic = true
					}
				}
				i++
			}
			continue
		}
		if strings.Contains(token, "=") {
			continue
		}
		return executableShellCommand{Args: args[i:], Dir: dir, DirDynamic: dirDynamic}, true
	}
	return executableShellCommand{}, false
}

func envOptionNeedsValue(token string) bool {
	return envOptionValueKind(token) != ""
}

func envOptionValueKind(token string) string {
	if strings.Contains(token, "=") {
		return ""
	}
	switch token {
	case "-C", "--chdir":
		return "directory"
	case "-u", "--unset", "-S", "--split-string":
		return "value"
	default:
		return ""
	}
}

func envInlineDirectoryValue(token string) (string, bool) {
	if strings.HasPrefix(token, "--chdir=") {
		return strings.TrimPrefix(token, "--chdir="), true
	}
	return "", false
}

func commandWrappedCommandArgs(args []shellWord) ([]shellWord, bool) {
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg.Dynamic {
			return nil, false
		}
		token := arg.Text
		if token == "" {
			continue
		}
		if token == "--" {
			return args[i+1:], true
		}
		if strings.HasPrefix(token, "-") {
			if strings.ContainsAny(token, "vV") {
				return nil, false
			}
			continue
		}
		return args[i:], true
	}
	return nil, false
}

func normalizeCommandInvocationToken(token string) string {
	if token == "" {
		return ""
	}
	trimmed := strings.TrimLeft(token, "@+-")
	if trimmed == "" {
		return token
	}
	return trimmed
}

const dynamicCommandTokenText = "__READYCI_DYNAMIC__"

type shellWord struct {
	Text    string
	Dynamic bool
}

type shellCommand struct {
	Args       []shellWord
	Dir        string
	DirDynamic bool
}

type shellScanContext struct {
	Dir        string
	DirDynamic bool
}

func shellCommandsFromCommand(command string) []shellCommand {
	command = normalizeShellCommandForParse(command)
	file, err := syntax.NewParser().Parse(strings.NewReader(command), "")
	if err != nil {
		return nil
	}
	var commands []shellCommand
	ctx := shellScanContext{}
	scanShellStatements(file.Stmts, &ctx, &commands)
	return commands
}

func scanShellStatements(stmts []*syntax.Stmt, ctx *shellScanContext, commands *[]shellCommand) {
	for _, stmt := range stmts {
		scanShellStatement(stmt, ctx, commands)
	}
}

func scanShellStatement(stmt *syntax.Stmt, ctx *shellScanContext, commands *[]shellCommand) {
	if stmt == nil || stmt.Cmd == nil {
		return
	}
	scanShellCommand(stmt.Cmd, ctx, commands)
}

func scanShellCommand(command syntax.Command, ctx *shellScanContext, commands *[]shellCommand) {
	switch node := command.(type) {
	case *syntax.CallExpr:
		scanShellCall(node, ctx, commands)
	case *syntax.BinaryCmd:
		scanShellBinary(node, ctx, commands)
	case *syntax.Subshell:
		child := *ctx
		scanShellStatements(node.Stmts, &child, commands)
	case *syntax.Block:
		scanShellStatements(node.Stmts, ctx, commands)
	case *syntax.IfClause:
		cond := *ctx
		scanShellStatements(node.Cond, &cond, commands)
		thenCtx := cond
		scanShellStatements(node.Then, &thenCtx, commands)
		if node.Else != nil {
			elseCtx := *ctx
			scanShellCommand(node.Else, &elseCtx, commands)
		}
	case *syntax.WhileClause:
		cond := *ctx
		scanShellStatements(node.Cond, &cond, commands)
		body := cond
		scanShellStatements(node.Do, &body, commands)
	case *syntax.ForClause:
		body := *ctx
		scanShellStatements(node.Do, &body, commands)
	case *syntax.CaseClause:
		for _, item := range node.Items {
			branch := *ctx
			scanShellStatements(item.Stmts, &branch, commands)
		}
	case *syntax.TimeClause:
		scanShellStatement(node.Stmt, ctx, commands)
	case *syntax.CoprocClause:
		child := *ctx
		scanShellStatement(node.Stmt, &child, commands)
	case *syntax.FuncDecl:
		child := *ctx
		scanShellStatement(node.Body, &child, commands)
	}
}

func scanShellBinary(node *syntax.BinaryCmd, ctx *shellScanContext, commands *[]shellCommand) {
	if node == nil {
		return
	}
	original := *ctx
	switch node.Op {
	case syntax.OrStmt:
		scanShellStatement(node.X, ctx, commands)
		right := original
		scanShellStatement(node.Y, &right, commands)
	case syntax.Pipe, syntax.PipeAll:
		left := original
		right := original
		scanShellStatement(node.X, &left, commands)
		scanShellStatement(node.Y, &right, commands)
	default:
		scanShellStatement(node.X, ctx, commands)
		scanShellStatement(node.Y, ctx, commands)
	}
}

func scanShellCall(call *syntax.CallExpr, ctx *shellScanContext, commands *[]shellCommand) {
	if call == nil || len(call.Args) == 0 {
		return
	}
	args := shellWordsFromSyntax(call.Args)
	commandName := ""
	commandDynamic := true
	if len(args) > 0 {
		commandName = normalizeCommandInvocationToken(args[0].Text)
		commandDynamic = args[0].Dynamic
	}
	if !commandDynamic && commandName == "cd" {
		if len(args) < 2 || args[1].Dynamic {
			ctx.DirDynamic = true
			return
		}
		nextDir, ok := resolveMakeDir(ctx.Dir, args[1].Text)
		if !ok {
			ctx.DirDynamic = true
			return
		}
		ctx.Dir = nextDir
		ctx.DirDynamic = false
		return
	}
	*commands = append(*commands, shellCommand{Args: args, Dir: ctx.Dir, DirDynamic: ctx.DirDynamic})
}

func shellWordsFromSyntax(words []*syntax.Word) []shellWord {
	out := make([]shellWord, 0, len(words))
	for _, word := range words {
		out = append(out, shellWordFromSyntax(word))
	}
	return out
}

func shellWordFromSyntax(word *syntax.Word) shellWord {
	if word == nil {
		return shellWord{}
	}
	text, dynamic := staticShellWordParts(word.Parts)
	if strings.Contains(text, dynamicCommandTokenText) {
		dynamic = true
	}
	return shellWord{Text: text, Dynamic: dynamic}
}

func staticShellWordParts(parts []syntax.WordPart) (string, bool) {
	var out strings.Builder
	for _, part := range parts {
		switch value := part.(type) {
		case *syntax.Lit:
			out.WriteString(value.Value)
		case *syntax.SglQuoted:
			out.WriteString(value.Value)
		case *syntax.DblQuoted:
			text, dynamic := staticShellWordParts(value.Parts)
			out.WriteString(text)
			if dynamic {
				return out.String(), true
			}
		default:
			return out.String(), true
		}
	}
	return out.String(), false
}

func normalizeShellCommandForParse(command string) string {
	command = normalizeShellLineContinuations(command)
	command = replaceGitHubActionsExpressions(command)
	command = strings.ReplaceAll(command, "$(READYCI)", "readyci")
	command = strings.ReplaceAll(command, "$(MAKE)", "make")
	return stripMakeRecipeCommandPrefixes(command)
}

func replaceGitHubActionsExpressions(command string) string {
	var out strings.Builder
	for i := 0; i < len(command); i++ {
		if strings.HasPrefix(command[i:], "${{") {
			end := strings.Index(command[i+3:], "}}")
			if end < 0 {
				out.WriteString(dynamicCommandTokenText)
				break
			}
			out.WriteString(dynamicCommandTokenText)
			i += 3 + end + len("}}") - 1
			continue
		}
		out.WriteByte(command[i])
	}
	return out.String()
}

func stripMakeRecipeCommandPrefixes(command string) string {
	var out strings.Builder
	atCommandStart := true
	for i := 0; i < len(command); i++ {
		ch := command[i]
		if atCommandStart {
			for i < len(command) && (command[i] == ' ' || command[i] == '\t') {
				out.WriteByte(command[i])
				i++
			}
			for i < len(command) && (command[i] == '@' || command[i] == '+' || command[i] == '-') {
				i++
			}
			if i >= len(command) {
				break
			}
			ch = command[i]
			atCommandStart = false
		}
		out.WriteByte(ch)
		switch ch {
		case '\n', ';':
			atCommandStart = true
		case '&':
			atCommandStart = true
			if i+1 < len(command) && command[i+1] == '&' {
				out.WriteByte(command[i+1])
				i++
			}
		case '|':
			atCommandStart = true
			if i+1 < len(command) && command[i+1] == '|' {
				out.WriteByte(command[i+1])
				i++
			}
		}
	}
	return out.String()
}
