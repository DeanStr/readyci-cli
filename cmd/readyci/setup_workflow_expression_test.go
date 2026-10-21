package main

import (
	"math"
	"reflect"
	"strings"
	"testing"
)

func setupWorkflowExpressionTestContexts() map[string]map[string]any {
	return map[string]map[string]any{
		"matrix": {"node": 24, "enabled": false, "zero": 0, "display-name": "unit"},
		"env":    {"FLAG": "false", "ZERO": "0", "Foo": "upper", "foo": "lower", "EXPR": "${{ secrets.KEY }}", "PATTERN": "{0}", "SHELL": "$(touch /tmp/example); echo \"$HOME\""},
		"runner": {"os": "Linux", "arch": "X64"},
	}
}

func TestSetupWorkflowExpressionTypedSemantics(t *testing.T) {
	for _, test := range []struct {
		expression string
		want       any
	}{
		{"matrix.node", float64(24)}, {"matrix.enabled", false},
		{"!matrix.enabled", true}, {"!!env.FLAG", true}, {"!!env.ZERO", true}, {"!!matrix.zero", false},
		{"!!false", false}, {"!!'false'", true}, {"!!0", false}, {"!!'0'", true}, {"!!null", false},
		{"false || 'fallback'", "fallback"}, {"'false' || 'fallback'", "false"},
		{"0 && 'unused'", float64(0)}, {"1 && 7", float64(7)},
		{"true && 'selected' || 'fallback'", "selected"},
		{"false && format('{5}', 'unused')", false}, {"true || format('{5}', 'unused')", true},
		{"null == false", true}, {"null == 0", true}, {"'' == 0", true}, {"'2' == 2", true}, {"true == 1", true},
		{"'word' == 0", false}, {"'word' != 0", true}, {"'A' == 'a'", true}, {"'2' == '02'", false}, {"'01' == '1'", false},
		{"true || false && false", true}, {"(true || false) && false", false}, {"!false == true", true},
		{"matrix.missing", ""}, {"matrix.NODE", float64(24)}, {"matrix.display-name", "unit"},
		{"env.Foo", "upper"}, {"env.foo", "lower"}, {"runner.OS == 'linux'", true},
		{"'it''s literal'", "it's literal"}, {"-2.5e-2", -0.025},
		{"format('{{{0}}}', matrix.node)", "{24}"},
		{"format('{0}/{1}/{2}', true, null, 12.5)", "true//12.5"},
		{"format('{0}{1}', '{1}', 'x')", "{1}x"},
		{"format('{0}', env.EXPR)", "${{ secrets.KEY }}"},
		{"env.SHELL", "$(touch /tmp/example); echo \"$HOME\""},
	} {
		t.Run(test.expression, func(t *testing.T) {
			got, err := setupEvaluateWorkflowExpression(test.expression, setupWorkflowExpressionTestContexts())
			if err != nil || !reflect.DeepEqual(got, test.want) {
				t.Fatalf("evaluate = %#v, %v; want %#v", got, err, test.want)
			}
		})
	}
}

func TestSetupWorkflowExpressionRejectsUnavailableOrUnsupported(t *testing.T) {
	for _, expression := range []string{
		"", "matrix", "matrix.node.value", "matrix['node']", "\"double quoted\"",
		"secrets.KEY", "github.ref", "inputs.test", "runner.arch", "runner.temp",
		"env.MISSING", "env.FOO", "true || env.MISSING",
		"false && secrets.KEY", "true || github.ref", "false && success()", "always()", "fromJSON('0')", "contains('x', 'x')",
		"format(env.PATTERN, 'x')", "format()", "format(1)", "format('{1}', 'x')", "format('{-1}', 'x')",
		"format('{9999}', 'x')", "format('{0:x}', 'x')", "format('{')", "format('}')", "format('{0}',)",
		"'0xff' == 255", "'01' == 1", "'01' != 1", "'+1' == 1", "'1.' == 1", "'.5' == 0.5", "'0o10' == 8",
		"0xff", "0xffffffff", "0Xff", "-0xff",
		"!", "true &&", "true & false", "1 + 2", "1 < 2", "[1]", "{}", "1e309", "NaN", "Infinity", "1.2.3",
	} {
		t.Run(expression, func(t *testing.T) {
			if got, err := setupEvaluateWorkflowExpression(expression, setupWorkflowExpressionTestContexts()); err == nil {
				t.Fatalf("accepted unsupported expression with result %#v", got)
			}
		})
	}
	for _, contexts := range []map[string]map[string]any{nil, {"runner": {}}, {"runner": {"os": nil}}, {"runner": {"os": 1}}} {
		if _, err := setupEvaluateWorkflowExpression("runner.os", contexts); err == nil {
			t.Fatal("accepted unavailable runner.os")
		}
	}
	if _, err := setupEvaluateWorkflowExpression("env.MISSING", map[string]map[string]any{}); err == nil {
		t.Fatal("accepted absent env context")
	}
	for _, value := range []any{[]string{"x"}, map[string]any{"x": 1}, math.NaN(), math.Inf(1)} {
		if _, err := setupEvaluateWorkflowExpression("matrix.value", map[string]map[string]any{"matrix": {"value": value}}); err == nil {
			t.Fatalf("accepted non-scalar or non-finite value %#v", value)
		}
	}
	if _, err := setupEvaluateWorkflowExpression("matrix.foo", map[string]map[string]any{"matrix": {"Foo": 1, "foo": 2}}); err == nil {
		t.Fatal("accepted ambiguous case-insensitive matrix property")
	}
}

func TestSetupWorkflowInterpolationIsScalarAndSinglePass(t *testing.T) {
	for _, test := range []struct{ text, want string }{
		{"node ${{ matrix.node }} / ${{ runner.os }}", "node 24 / Linux"},
		{"${{ false }}:${{ 0 }}:${{ null }}:${{ 1.5 }}", "false:0::1.5"},
		{"${{ 1000000000000000 }}:${{ 0.00001 }}:${{ 1.2345678901234567 }}", "1E+15:1E-05:1.23456789012346"},
		{"echo ${{ env.SHELL }}", "echo $(touch /tmp/example); echo \"$HOME\""},
		{"${{ env.EXPR }}", "${{ secrets.KEY }}"},
		{"${{ format('{0}', env.EXPR) }}", "${{ secrets.KEY }}"},
		{"${{ 'it''s }} literal' }}", "it's }} literal"},
		{"${{ format('a}}b', 'unused') }}", "a}b"},
		{"plain }} shell text", "plain }} shell text"},
	} {
		t.Run(test.text, func(t *testing.T) {
			got, err := setupInterpolateWorkflowExpressions(test.text, setupWorkflowExpressionTestContexts())
			if err != nil || got != test.want {
				t.Fatalf("interpolate = %q, %v; want %q", got, err, test.want)
			}
		})
	}
	for _, text := range []string{"${{", "${{ 'unclosed }}", "${{ matrix.node }", "${{ }}", "${{ secrets.KEY }}"} {
		if _, err := setupInterpolateWorkflowExpressions(text, setupWorkflowExpressionTestContexts()); err == nil {
			t.Errorf("accepted invalid interpolation %q", text)
		}
	}
}

func TestSetupWorkflowExpressionBounds(t *testing.T) {
	for _, expression := range []string{
		"'" + strings.Repeat("x", setupWorkflowExpressionMaxChars) + "'",
		strings.Repeat("!", setupWorkflowExpressionMaxDepth+1) + "true",
		strings.Repeat("(", setupWorkflowExpressionMaxDepth+1) + "true" + strings.Repeat(")", setupWorkflowExpressionMaxDepth+1),
		strings.Repeat("0 == ", setupWorkflowExpressionMaxTokens) + "0",
		"format('x', " + strings.Repeat("0, ", 33) + "0)",
		"format('" + strings.Repeat("{0}", 257) + "', 'x')",
	} {
		if _, err := setupEvaluateWorkflowExpression(expression, setupWorkflowExpressionTestContexts()); err == nil {
			t.Fatal("accepted expression exceeding a bound")
		}
	}
	unicodeLiteral := "'" + strings.Repeat("界", setupWorkflowExpressionMaxChars-2) + "'"
	if _, err := setupEvaluateWorkflowExpression(unicodeLiteral, nil); err != nil {
		t.Fatalf("character bound counted UTF-8 bytes: %v", err)
	}
	contexts := map[string]map[string]any{"env": {"LARGE": strings.Repeat("x", setupWorkflowExpressionMaxOutput/2+1)}}
	if _, err := setupEvaluateWorkflowExpression("format('{0}{0}', env.LARGE)", contexts); err == nil {
		t.Fatal("accepted oversized format output")
	}
	if _, err := setupInterpolateWorkflowExpressions("${{ env.LARGE }}${{ env.LARGE }}", contexts); err == nil {
		t.Fatal("accepted oversized interpolation output")
	}
	if _, err := setupInterpolateWorkflowExpressions(strings.Repeat("${{ 0 }}", 129), nil); err == nil {
		t.Fatal("accepted too many interpolations")
	}
}
