package dot

import (
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestLexer_Tokenize(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  []TokenType
	}{
		{
			name:  "empty",
			input: "",
			want:  []TokenType{TokenEOF},
		},
		{
			name:  "minimal digraph",
			input: `digraph G { }`,
			want:  []TokenType{TokenDigraph, TokenIdent, TokenLBrace, TokenRBrace, TokenEOF},
		},
		{
			name:  "node with attributes",
			input: `start [shape=Mdiamond, label="Start"]`,
			want: []TokenType{
				TokenIdent, TokenLBracket,
				TokenIdent, TokenEquals, TokenIdent, TokenComma,
				TokenIdent, TokenEquals, TokenString,
				TokenRBracket, TokenEOF,
			},
		},
		{
			name:  "edge with arrow",
			input: `A -> B`,
			want:  []TokenType{TokenIdent, TokenArrow, TokenIdent, TokenEOF},
		},
		{
			name:  "chained edge",
			input: `A -> B -> C`,
			want:  []TokenType{TokenIdent, TokenArrow, TokenIdent, TokenArrow, TokenIdent, TokenEOF},
		},
		{
			name:  "keywords",
			input: `digraph subgraph node edge graph true false`,
			want: []TokenType{
				TokenDigraph, TokenSubgraph, TokenNode, TokenEdge,
				TokenGraphKW, TokenTrue, TokenFalse, TokenEOF,
			},
		},
		{
			name:  "numbers",
			input: `42 -1 3.14 0.5`,
			want:  []TokenType{TokenNumber, TokenNumber, TokenNumber, TokenNumber, TokenEOF},
		},
		{
			name:  "string with escapes",
			input: `"hello\nworld" "with \"quotes\""`,
			want:  []TokenType{TokenString, TokenString, TokenEOF},
		},
		{
			name:  "semicolons optional",
			input: `A; B`,
			want:  []TokenType{TokenIdent, TokenSemicolon, TokenIdent, TokenEOF},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tokens, err := NewLexer(tt.input).Tokenize()
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			got := make([]TokenType, len(tokens))
			for i, tok := range tokens {
				got[i] = tok.Type
			}
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("token types mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestLexer_StringValues(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "simple", input: `"hello"`, want: "hello"},
		{name: "newline escape", input: `"line1\nline2"`, want: "line1\nline2"},
		{name: "tab escape", input: `"col1\tcol2"`, want: "col1\tcol2"},
		{name: "escaped quote", input: `"say \"hi\""`, want: `say "hi"`},
		{name: "backslash", input: `"path\\file"`, want: `path\file`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tokens, err := NewLexer(tt.input).Tokenize()
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tokens[0].Type != TokenString {
				t.Fatalf("expected TokenString, got %d", tokens[0].Type)
			}
			if diff := cmp.Diff(tt.want, tokens[0].Val); diff != "" {
				t.Errorf("string value mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestLexer_CommentStripping(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  []TokenType
	}{
		{
			name:  "line comment",
			input: "A // this is a comment\nB",
			want:  []TokenType{TokenIdent, TokenIdent, TokenEOF},
		},
		{
			name:  "block comment",
			input: "A /* skip this */ B",
			want:  []TokenType{TokenIdent, TokenIdent, TokenEOF},
		},
		{
			name:  "multiline block comment",
			input: "A /* skip\nthis\ntoo */ B",
			want:  []TokenType{TokenIdent, TokenIdent, TokenEOF},
		},
		{
			name:  "comment chars inside string are preserved",
			input: `"// not a comment" "/* also not */"`,
			want:  []TokenType{TokenString, TokenString, TokenEOF},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tokens, err := NewLexer(tt.input).Tokenize()
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			got := make([]TokenType, len(tokens))
			for i, tok := range tokens {
				got[i] = tok.Type
			}
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("token types mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestLexer_Errors(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{name: "unterminated string", input: `"hello`},
		{name: "unexpected character", input: `@`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewLexer(tt.input).Tokenize()
			if err == nil {
				t.Error("expected error, got nil")
			}
		})
	}
}

func TestLexer_LineCol(t *testing.T) {
	input := "digraph G {\n  A\n}"
	tokens, err := NewLexer(input).Tokenize()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// "A" is on line 2, col 3
	aToken := tokens[3] // digraph(1,1) G(1,9) {(1,11) A(2,3)
	if aToken.Val != "A" {
		t.Fatalf("expected token A, got %q", aToken.Val)
	}
	if aToken.Line != 2 || aToken.Col != 3 {
		t.Errorf("expected A at line 2 col 3, got line %d col %d", aToken.Line, aToken.Col)
	}
}
