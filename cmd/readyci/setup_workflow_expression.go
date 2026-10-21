package main

import (
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"strconv"
	"strings"
	"unicode/utf8"
)

const (
	setupWorkflowExpressionMaxChars  = 4096
	setupWorkflowExpressionMaxTokens = 512
	setupWorkflowExpressionMaxDepth  = 32
	setupWorkflowExpressionMaxOutput = 64 * 1024
)

type setupWorkflowExpressionToken struct {
	kind  string
	text  string
	value any
}

type setupWorkflowExpressionNode struct {
	kind        string
	value       any
	left, right *setupWorkflowExpressionNode
	args        []*setupWorkflowExpressionNode
	depth       int
}

type setupWorkflowExpressionParser struct {
	tokens   []setupWorkflowExpressionToken
	position int
	depth    int
	contexts map[string]map[string]any
}

// setupEvaluateWorkflowExpression evaluates the supported static expression
// body. Context access is limited to matrix, env, and the known runner.os value.
func setupEvaluateWorkflowExpression(expression string, contexts map[string]map[string]any) (any, error) {
	if !utf8.ValidString(expression) || utf8.RuneCountInString(expression) > setupWorkflowExpressionMaxChars {
		return nil, fmt.Errorf("workflow expression exceeds %d characters or is invalid UTF-8", setupWorkflowExpressionMaxChars)
	}
	tokens, err := setupWorkflowExpressionTokens(expression)
	if err != nil {
		return nil, err
	}
	parser := setupWorkflowExpressionParser{tokens: tokens, contexts: contexts}
	node, err := parser.parseOr()
	if err != nil {
		return nil, err
	}
	if parser.peek().kind != "end" {
		return nil, fmt.Errorf("unexpected workflow expression token %q", parser.peek().text)
	}
	return node.evaluate()
}

// setupInterpolateWorkflowExpressions scans the original text once. Returned
// strings are never reparsed as expressions or escaped as shell syntax.
func setupInterpolateWorkflowExpressions(text string, contexts map[string]map[string]any) (string, error) {
	if len(text) > setupWorkflowExpressionMaxOutput || !utf8.ValidString(text) {
		return "", fmt.Errorf("workflow interpolation input is too large or invalid UTF-8")
	}
	var output strings.Builder
	for count := 0; ; count++ {
		start := strings.Index(text, "${{")
		if start < 0 {
			if output.Len()+len(text) > setupWorkflowExpressionMaxOutput {
				return "", fmt.Errorf("workflow interpolation output is too large")
			}
			output.WriteString(text)
			return output.String(), nil
		}
		if count >= 128 {
			return "", fmt.Errorf("too many workflow interpolations")
		}
		end, quoted := -1, false
		for i := start + 3; i < len(text); i++ {
			if text[i] == '\'' {
				if quoted && i+1 < len(text) && text[i+1] == '\'' {
					i++
					continue
				}
				quoted = !quoted
			} else if !quoted && text[i] == '}' && i+1 < len(text) && text[i+1] == '}' {
				end = i
				break
			}
		}
		if end < 0 {
			return "", fmt.Errorf("unclosed workflow expression")
		}
		value, err := setupEvaluateWorkflowExpression(text[start+3:end], contexts)
		if err != nil {
			return "", err
		}
		rendered, err := setupWorkflowExpressionString(value)
		if err != nil {
			return "", err
		}
		if output.Len()+start+len(rendered) > setupWorkflowExpressionMaxOutput {
			return "", fmt.Errorf("workflow interpolation output is too large")
		}
		output.WriteString(text[:start])
		output.WriteString(rendered)
		text = text[end+2:]
	}
}

func setupWorkflowExpressionTokens(text string) ([]setupWorkflowExpressionToken, error) {
	var tokens []setupWorkflowExpressionToken
	for i := 0; i < len(text); {
		if strings.ContainsRune(" \t\r\n", rune(text[i])) {
			i++
			continue
		}
		if len(tokens) >= setupWorkflowExpressionMaxTokens {
			return nil, fmt.Errorf("too many workflow expression tokens")
		}
		start := i
		token := setupWorkflowExpressionToken{}
		switch {
		case text[i] == '\'':
			i++
			var value strings.Builder
			closed := false
			for i < len(text) {
				if text[i] != '\'' {
					value.WriteByte(text[i])
					i++
					continue
				}
				i++
				if i < len(text) && text[i] == '\'' {
					value.WriteByte('\'')
					i++
					continue
				}
				closed = true
				break
			}
			if !closed {
				return nil, fmt.Errorf("unclosed workflow expression string")
			}
			token.kind, token.value = "string", value.String()
		case setupWorkflowExpressionIdentifierStart(text[i]):
			i++
			for i < len(text) && (setupWorkflowExpressionIdentifierStart(text[i]) || text[i] >= '0' && text[i] <= '9' || text[i] == '-') {
				i++
			}
			token.kind = "identifier"
		case text[i] >= '0' && text[i] <= '9' || text[i] == '-':
			i++
			for i < len(text) && !strings.ContainsRune(" \t\r\n(),=!&|", rune(text[i])) {
				i++
			}
			number, err := setupWorkflowExpressionNumber(text[start:i])
			if err != nil {
				return nil, fmt.Errorf("invalid finite workflow number %q", text[start:i])
			}
			token.kind, token.value = "number", number
		default:
			if i+1 < len(text) && (text[i:i+2] == "==" || text[i:i+2] == "!=" || text[i:i+2] == "&&" || text[i:i+2] == "||") {
				token.kind = text[i : i+2]
				i += 2
			} else if strings.ContainsRune("!().,", rune(text[i])) {
				token.kind = text[i : i+1]
				i++
			} else {
				return nil, fmt.Errorf("unsupported workflow expression character %q", text[i])
			}
		}
		token.text = text[start:i]
		tokens = append(tokens, token)
	}
	return append(tokens, setupWorkflowExpressionToken{kind: "end"}), nil
}

func setupWorkflowExpressionIdentifierStart(ch byte) bool {
	return ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch == '_'
}

func (parser *setupWorkflowExpressionParser) peek() setupWorkflowExpressionToken {
	return parser.tokens[parser.position]
}

func (parser *setupWorkflowExpressionParser) take(kind string) bool {
	if parser.peek().kind != kind {
		return false
	}
	parser.position++
	return true
}

func (parser *setupWorkflowExpressionParser) parseOr() (*setupWorkflowExpressionNode, error) {
	return parser.parseBinary(parser.parseAnd, "||")
}

func (parser *setupWorkflowExpressionParser) parseAnd() (*setupWorkflowExpressionNode, error) {
	return parser.parseBinary(parser.parseEquality, "&&")
}

func (parser *setupWorkflowExpressionParser) parseEquality() (*setupWorkflowExpressionNode, error) {
	return parser.parseBinary(parser.parseUnary, "==", "!=")
}

func (parser *setupWorkflowExpressionParser) parseBinary(next func() (*setupWorkflowExpressionNode, error), operators ...string) (*setupWorkflowExpressionNode, error) {
	left, err := next()
	if err != nil {
		return nil, err
	}
	for {
		op := parser.peek().kind
		matched := false
		for _, candidate := range operators {
			matched = matched || op == candidate
		}
		if !matched {
			return left, nil
		}
		parser.position++
		right, err := next()
		if err != nil {
			return nil, err
		}
		left = &setupWorkflowExpressionNode{kind: op, left: left, right: right, depth: max(left.depth, right.depth) + 1}
		if left.depth > setupWorkflowExpressionMaxDepth {
			return nil, fmt.Errorf("workflow expression nesting is too deep")
		}
	}
}

func (parser *setupWorkflowExpressionParser) parseUnary() (*setupWorkflowExpressionNode, error) {
	parser.depth++
	defer func() { parser.depth-- }()
	if parser.depth > setupWorkflowExpressionMaxDepth {
		return nil, fmt.Errorf("workflow expression nesting is too deep")
	}
	if parser.take("!") {
		child, err := parser.parseUnary()
		if err != nil {
			return nil, err
		}
		return &setupWorkflowExpressionNode{kind: "!", left: child, depth: child.depth + 1}, nil
	}
	if parser.take("(") {
		node, err := parser.parseOr()
		if err != nil {
			return nil, err
		}
		if !parser.take(")") {
			return nil, fmt.Errorf("missing workflow expression closing parenthesis")
		}
		return node, nil
	}
	token := parser.peek()
	if token.kind == "number" || token.kind == "string" {
		parser.position++
		return &setupWorkflowExpressionNode{kind: "literal", value: token.value, depth: 1}, nil
	}
	if token.kind != "identifier" {
		return nil, fmt.Errorf("expected workflow expression value, found %q", token.text)
	}
	parser.position++
	name := strings.ToLower(token.text)
	if parser.take("(") {
		if name != "format" {
			return nil, fmt.Errorf("unsupported workflow expression function %q", token.text)
		}
		node := &setupWorkflowExpressionNode{kind: "format", depth: 1}
		if !parser.take(")") {
			for {
				arg, err := parser.parseOr()
				if err != nil {
					return nil, err
				}
				node.args = append(node.args, arg)
				node.depth = max(node.depth, arg.depth+1)
				if len(node.args) > 33 || node.depth > setupWorkflowExpressionMaxDepth {
					return nil, fmt.Errorf("workflow format arguments or nesting exceed limits")
				}
				if parser.take(")") {
					break
				}
				if !parser.take(",") {
					return nil, fmt.Errorf("expected workflow format argument separator")
				}
			}
		}
		if len(node.args) == 0 || node.args[0].kind != "literal" {
			return nil, fmt.Errorf("workflow format requires a literal format string")
		}
		if _, ok := node.args[0].value.(string); !ok {
			return nil, fmt.Errorf("workflow format requires a literal format string")
		}
		return node, nil
	}
	if name == "true" || name == "false" || name == "null" {
		var value any
		if name != "null" {
			value = name == "true"
		}
		return &setupWorkflowExpressionNode{kind: "literal", value: value, depth: 1}, nil
	}
	if name != "matrix" && name != "env" && name != "runner" {
		return nil, fmt.Errorf("unsupported workflow expression context %q", token.text)
	}
	if !parser.take(".") || parser.peek().kind != "identifier" {
		return nil, fmt.Errorf("workflow context %s requires a named property", name)
	}
	property := parser.peek().text
	parser.position++
	if name == "runner" && !strings.EqualFold(property, "os") {
		return nil, fmt.Errorf("runner.%s is unavailable during static workflow setup", property)
	}
	context, present := parser.contexts[name]
	if !present {
		return nil, fmt.Errorf("workflow context %s is unavailable", name)
	}
	value, found, err := setupWorkflowExpressionProperty(context, property, name == "env")
	if err != nil {
		return nil, err
	}
	if name == "runner" {
		os, ok := value.(string)
		if !found || !ok || os == "" {
			return nil, fmt.Errorf("runner.os is unavailable during static workflow setup")
		}
	} else if name == "env" && !found {
		return nil, fmt.Errorf("env.%s is not statically known; runner values and earlier environment exports require explicit configuration", property)
	} else if !found {
		value = ""
	}
	value, err = setupWorkflowExpressionScalar(value)
	if err != nil {
		return nil, fmt.Errorf("workflow context %s.%s: %w", name, property, err)
	}
	return &setupWorkflowExpressionNode{kind: "context", value: value, depth: 1}, nil
}

func setupWorkflowExpressionProperty(context map[string]any, property string, caseSensitive bool) (any, bool, error) {
	if caseSensitive {
		value, found := context[property]
		return value, found, nil
	}
	var value any
	found := false
	for key, candidate := range context {
		if strings.EqualFold(key, property) {
			if found {
				return nil, false, fmt.Errorf("ambiguous workflow context property %q", property)
			}
			value, found = candidate, true
		}
	}
	return value, found, nil
}

func setupWorkflowExpressionScalar(value any) (any, error) {
	switch v := value.(type) {
	case nil, bool:
		return value, nil
	case string:
		if len(v) > setupWorkflowExpressionMaxOutput || !utf8.ValidString(v) {
			return nil, fmt.Errorf("string value is too large or invalid UTF-8")
		}
		return v, nil
	case json.Number:
		return setupWorkflowExpressionNumber(string(v))
	}
	reflected := reflect.ValueOf(value)
	var number float64
	switch reflected.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		number = float64(reflected.Int())
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		number = float64(reflected.Uint())
	case reflect.Float32, reflect.Float64:
		number = reflected.Float()
	default:
		return nil, fmt.Errorf("only scalar workflow context values are supported")
	}
	if math.IsInf(number, 0) || math.IsNaN(number) {
		return nil, fmt.Errorf("workflow numbers must be finite")
	}
	return number, nil
}

func setupWorkflowExpressionNumber(text string) (float64, error) {
	var value any
	if err := json.Unmarshal([]byte(text), &value); err != nil {
		return 0, err
	}
	if number, ok := value.(float64); ok && !math.IsNaN(number) && !math.IsInf(number, 0) {
		return number, nil
	}
	return 0, fmt.Errorf("expected a finite number")
}

func (node *setupWorkflowExpressionNode) evaluate() (any, error) {
	if node.kind == "literal" || node.kind == "context" {
		return node.value, nil
	}
	if node.kind == "format" {
		var arguments []any
		for _, arg := range node.args[1:] {
			value, err := arg.evaluate()
			if err != nil {
				return nil, err
			}
			arguments = append(arguments, value)
		}
		return setupWorkflowExpressionFormat(node.args[0].value.(string), arguments)
	}
	left, err := node.left.evaluate()
	if err != nil {
		return nil, err
	}
	switch node.kind {
	case "!":
		return !setupWorkflowExpressionTruthy(left), nil
	case "&&":
		if !setupWorkflowExpressionTruthy(left) {
			return left, nil
		}
	case "||":
		if setupWorkflowExpressionTruthy(left) {
			return left, nil
		}
	}
	right, err := node.right.evaluate()
	if err != nil {
		return nil, err
	}
	if node.kind == "&&" || node.kind == "||" {
		return right, nil
	}
	equal, err := setupWorkflowExpressionEqual(left, right)
	return equal != (node.kind == "!="), err
}

func setupWorkflowExpressionTruthy(value any) bool {
	switch v := value.(type) {
	case nil:
		return false
	case bool:
		return v
	case float64:
		return v != 0
	case string:
		return v != ""
	default:
		return false
	}
}

func setupWorkflowExpressionEqual(left, right any) (bool, error) {
	if l, ok := left.(string); ok {
		if r, ok := right.(string); ok {
			return strings.EqualFold(l, r), nil
		}
	}
	if left == nil && right == nil {
		return true, nil
	}
	number := func(value any) (float64, error) {
		switch v := value.(type) {
		case nil:
			return 0, nil
		case bool:
			if v {
				return 1, nil
			}
			return 0, nil
		case float64:
			return v, nil
		case string:
			if strings.TrimSpace(v) == "" {
				return 0, nil
			}
			if n, err := setupWorkflowExpressionNumber(strings.TrimSpace(v)); err == nil {
				return n, nil
			}
			unsigned := strings.TrimLeft(strings.TrimSpace(v), "+-")
			if len(unsigned) > 0 && (unsigned[0] >= '0' && unsigned[0] <= '9' || unsigned[0] == '.' && len(unsigned) > 1 && unsigned[1] >= '0' && unsigned[1] <= '9') {
				return 0, fmt.Errorf("unsupported numeric-string coercion %q; use a JSON number", v)
			}
		}
		return math.NaN(), nil
	}
	l, err := number(left)
	if err != nil {
		return false, err
	}
	r, err := number(right)
	return l == r, err
}

func setupWorkflowExpressionString(value any) (string, error) {
	switch v := value.(type) {
	case nil:
		return "", nil
	case string:
		return v, nil
	case bool:
		return strconv.FormatBool(v), nil
	case float64:
		return strconv.FormatFloat(v, 'G', 15, 64), nil
	default:
		return "", fmt.Errorf("unsupported workflow scalar")
	}
}

func setupWorkflowExpressionFormat(format string, arguments []any) (string, error) {
	var output strings.Builder
	placeholders := 0
	for i := 0; i < len(format); {
		var part string
		switch {
		case strings.HasPrefix(format[i:], "{{") || strings.HasPrefix(format[i:], "}}"):
			part = format[i : i+1]
			i += 2
		case format[i] == '{':
			end := strings.IndexByte(format[i+1:], '}')
			if end < 0 {
				return "", fmt.Errorf("unclosed workflow format placeholder")
			}
			end += i + 1
			indexText := format[i+1 : end]
			if indexText == "" || len(indexText) > 3 || strings.Trim(indexText, "0123456789") != "" {
				return "", fmt.Errorf("unsupported workflow format placeholder")
			}
			index, err := strconv.Atoi(indexText)
			if err != nil || index >= len(arguments) {
				return "", fmt.Errorf("workflow format argument is unavailable")
			}
			placeholders++
			if placeholders > 256 {
				return "", fmt.Errorf("too many workflow format placeholders")
			}
			part, err = setupWorkflowExpressionString(arguments[index])
			if err != nil {
				return "", err
			}
			i = end + 1
		case format[i] == '}':
			return "", fmt.Errorf("unescaped workflow format closing brace")
		default:
			part = format[i : i+1]
			i++
		}
		if output.Len()+len(part) > setupWorkflowExpressionMaxOutput {
			return "", fmt.Errorf("workflow format output is too large")
		}
		output.WriteString(part)
	}
	return output.String(), nil
}
