package dot

import (
	"fmt"
	"strings"
	"unicode"
)

// TokenType classifies a lexer token.
type TokenType int

const (
	TokenEOF        TokenType = iota
	TokenDigraph              // "digraph"
	TokenSubgraph             // "subgraph"
	TokenNode                 // "node"  (default block keyword)
	TokenEdge                 // "edge"  (default block keyword)
	TokenGraphKW              // "graph" (default block keyword)
	TokenTrue                 // "true"
	TokenFalse                // "false"
	TokenIdent                // bare identifier
	TokenString               // double-quoted string
	TokenNumber               // integer or float literal
	TokenLBrace               // {
	TokenRBrace               // }
	TokenLBracket             // [
	TokenRBracket             // ]
	TokenArrow                // ->
	TokenEquals               // =
	TokenComma                // ,
	TokenSemicolon            // ;
)

// Token is a single lexical unit produced by the lexer.
type Token struct {
	Type TokenType
	Val  string
	Line int
	Col  int
}

func (t Token) String() string {
	if t.Type == TokenEOF {
		return "EOF"
	}
	return fmt.Sprintf("%q", t.Val)
}

// Lexer tokenizes a DOT source string.
type Lexer struct {
	src  []rune
	pos  int
	line int
	col  int
}

// NewLexer creates a lexer for the given DOT source.
func NewLexer(src string) *Lexer {
	cleaned := stripComments(src)
	return &Lexer{src: []rune(cleaned), line: 1, col: 1}
}

// Tokenize consumes the entire source and returns all tokens.
func (l *Lexer) Tokenize() ([]Token, error) {
	var tokens []Token
	for {
		tok, err := l.Next()
		if err != nil {
			return nil, err
		}
		tokens = append(tokens, tok)
		if tok.Type == TokenEOF {
			break
		}
	}
	return tokens, nil
}

// Next returns the next token from the source.
func (l *Lexer) Next() (Token, error) {
	l.skipWhitespace()
	if l.pos >= len(l.src) {
		return Token{Type: TokenEOF, Line: l.line, Col: l.col}, nil
	}

	ch := l.src[l.pos]
	startLine, startCol := l.line, l.col

	switch ch {
	case '{':
		l.advance()
		return Token{Type: TokenLBrace, Val: "{", Line: startLine, Col: startCol}, nil
	case '}':
		l.advance()
		return Token{Type: TokenRBrace, Val: "}", Line: startLine, Col: startCol}, nil
	case '[':
		l.advance()
		return Token{Type: TokenLBracket, Val: "[", Line: startLine, Col: startCol}, nil
	case ']':
		l.advance()
		return Token{Type: TokenRBracket, Val: "]", Line: startLine, Col: startCol}, nil
	case '=':
		l.advance()
		return Token{Type: TokenEquals, Val: "=", Line: startLine, Col: startCol}, nil
	case ',':
		l.advance()
		return Token{Type: TokenComma, Val: ",", Line: startLine, Col: startCol}, nil
	case ';':
		l.advance()
		return Token{Type: TokenSemicolon, Val: ";", Line: startLine, Col: startCol}, nil
	case '-':
		if l.pos+1 < len(l.src) && l.src[l.pos+1] == '>' {
			l.advance()
			l.advance()
			return Token{Type: TokenArrow, Val: "->", Line: startLine, Col: startCol}, nil
		}
		// Negative number
		return l.readNumber()
	case '"':
		return l.readString()
	}

	if unicode.IsDigit(ch) {
		return l.readNumber()
	}

	if isIdentStart(ch) {
		return l.readIdent()
	}

	return Token{}, fmt.Errorf("line %d col %d: unexpected character %q", l.line, l.col, string(ch))
}

func (l *Lexer) advance() {
	if l.pos < len(l.src) {
		if l.src[l.pos] == '\n' {
			l.line++
			l.col = 1
		} else {
			l.col++
		}
		l.pos++
	}
}

func (l *Lexer) peek() rune {
	if l.pos >= len(l.src) {
		return 0
	}
	return l.src[l.pos]
}

func (l *Lexer) skipWhitespace() {
	for l.pos < len(l.src) && unicode.IsSpace(l.src[l.pos]) {
		l.advance()
	}
}

func (l *Lexer) readString() (Token, error) {
	startLine, startCol := l.line, l.col
	l.advance() // skip opening "
	var sb strings.Builder
	for l.pos < len(l.src) {
		ch := l.src[l.pos]
		if ch == '\\' {
			l.advance()
			if l.pos >= len(l.src) {
				return Token{}, fmt.Errorf("line %d col %d: unterminated string escape", l.line, l.col)
			}
			esc := l.src[l.pos]
			switch esc {
			case '"':
				sb.WriteRune('"')
			case 'n':
				sb.WriteRune('\n')
			case 't':
				sb.WriteRune('\t')
			case '\\':
				sb.WriteRune('\\')
			default:
				sb.WriteRune('\\')
				sb.WriteRune(esc)
			}
			l.advance()
			continue
		}
		if ch == '"' {
			l.advance() // skip closing "
			return Token{Type: TokenString, Val: sb.String(), Line: startLine, Col: startCol}, nil
		}
		sb.WriteRune(ch)
		l.advance()
	}
	return Token{}, fmt.Errorf("line %d col %d: unterminated string", startLine, startCol)
}

func (l *Lexer) readNumber() (Token, error) {
	startLine, startCol := l.line, l.col
	start := l.pos
	if l.peek() == '-' {
		l.advance()
	}
	for l.pos < len(l.src) && unicode.IsDigit(l.src[l.pos]) {
		l.advance()
	}
	if l.pos < len(l.src) && l.src[l.pos] == '.' {
		l.advance()
		for l.pos < len(l.src) && unicode.IsDigit(l.src[l.pos]) {
			l.advance()
		}
	}
	val := string(l.src[start:l.pos])
	return Token{Type: TokenNumber, Val: val, Line: startLine, Col: startCol}, nil
}

func (l *Lexer) readIdent() (Token, error) {
	startLine, startCol := l.line, l.col
	start := l.pos
	for l.pos < len(l.src) && isIdentContinue(l.src[l.pos]) {
		l.advance()
	}
	val := string(l.src[start:l.pos])
	typ := classifyKeyword(val)
	return Token{Type: typ, Val: val, Line: startLine, Col: startCol}, nil
}

func classifyKeyword(s string) TokenType {
	switch s {
	case "digraph":
		return TokenDigraph
	case "subgraph":
		return TokenSubgraph
	case "node":
		return TokenNode
	case "edge":
		return TokenEdge
	case "graph":
		return TokenGraphKW
	case "true":
		return TokenTrue
	case "false":
		return TokenFalse
	default:
		return TokenIdent
	}
}

func isIdentStart(r rune) bool {
	return r == '_' || unicode.IsLetter(r)
}

func isIdentContinue(r rune) bool {
	return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r)
}

// stripComments removes // line comments and /* block */ comments from the
// source, replacing them with spaces to preserve line/col tracking.
func stripComments(src string) string {
	runes := []rune(src)
	out := make([]rune, len(runes))
	i := 0
	for i < len(runes) {
		if i+1 < len(runes) && runes[i] == '/' && runes[i+1] == '/' {
			// Line comment: replace until newline
			for i < len(runes) && runes[i] != '\n' {
				out[i] = ' '
				i++
			}
			continue
		}
		if i+1 < len(runes) && runes[i] == '/' && runes[i+1] == '*' {
			// Block comment: replace until */
			out[i] = ' '
			i++
			out[i] = ' '
			i++
			for i < len(runes) {
				if i+1 < len(runes) && runes[i] == '*' && runes[i+1] == '/' {
					out[i] = ' '
					i++
					out[i] = ' '
					i++
					break
				}
				if runes[i] == '\n' {
					out[i] = '\n' // preserve newlines for line tracking
				} else {
					out[i] = ' '
				}
				i++
			}
			continue
		}
		// Inside a quoted string, pass through as-is (don't strip // or /* inside strings)
		if runes[i] == '"' {
			out[i] = runes[i]
			i++
			for i < len(runes) && runes[i] != '"' {
				if runes[i] == '\\' && i+1 < len(runes) {
					out[i] = runes[i]
					i++
					out[i] = runes[i]
					i++
					continue
				}
				out[i] = runes[i]
				i++
			}
			if i < len(runes) {
				out[i] = runes[i]
				i++
			}
			continue
		}
		out[i] = runes[i]
		i++
	}
	return string(out[:i])
}
