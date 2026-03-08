package dot

import "fmt"

// Parse parses a DOT digraph source string into a Graph. Only directed graphs
// (digraph) are accepted; the parser enforces the strict Attractor DOT subset.
func Parse(src string) (*Graph, error) {
	tokens, err := NewLexer(src).Tokenize()
	if err != nil {
		return nil, fmt.Errorf("lex error: %w", err)
	}
	p := &parser{tokens: tokens}
	return p.parseGraph()
}

const maxSubgraphDepth = 50

type parser struct {
	tokens []Token
	pos    int
	depth  int

	// nodeDefaults and edgeDefaults track default attribute blocks.
	nodeDefaults map[string]string
	edgeDefaults map[string]string
}

func (p *parser) peek() Token {
	if p.pos >= len(p.tokens) {
		return Token{Type: TokenEOF}
	}
	return p.tokens[p.pos]
}

func (p *parser) advance() Token {
	tok := p.peek()
	if p.pos < len(p.tokens) {
		p.pos++
	}
	return tok
}

func (p *parser) expect(typ TokenType) (Token, error) {
	tok := p.advance()
	if tok.Type != typ {
		return tok, fmt.Errorf("line %d col %d: expected %d, got %s", tok.Line, tok.Col, typ, tok)
	}
	return tok, nil
}

func (p *parser) skipSemicolons() {
	for p.peek().Type == TokenSemicolon {
		p.advance()
	}
}

// parseGraph expects: 'digraph' Identifier '{' Statement* '}'
func (p *parser) parseGraph() (*Graph, error) {
	if _, err := p.expect(TokenDigraph); err != nil {
		return nil, fmt.Errorf("expected 'digraph': %w", err)
	}

	nameTok := p.advance()
	if nameTok.Type != TokenIdent && nameTok.Type != TokenString {
		return nil, fmt.Errorf("line %d col %d: expected graph name, got %s", nameTok.Line, nameTok.Col, nameTok)
	}

	if _, err := p.expect(TokenLBrace); err != nil {
		return nil, err
	}

	g := &Graph{
		Name:  nameTok.Val,
		Attrs: make(map[string]string),
	}
	p.nodeDefaults = make(map[string]string)
	p.edgeDefaults = make(map[string]string)

	if err := p.parseStatements(g); err != nil {
		return nil, err
	}

	if _, err := p.expect(TokenRBrace); err != nil {
		return nil, err
	}

	return g, nil
}

func (p *parser) parseStatements(g *Graph) error {
	for p.peek().Type != TokenRBrace && p.peek().Type != TokenEOF {
		if err := p.parseStatement(g); err != nil {
			return err
		}
		p.skipSemicolons()
	}
	return nil
}

func (p *parser) parseStatement(g *Graph) error {
	tok := p.peek()

	switch tok.Type {
	case TokenGraphKW:
		return p.parseGraphAttrStmt(g)
	case TokenNode:
		return p.parseNodeDefaults()
	case TokenEdge:
		return p.parseEdgeDefaults()
	case TokenSubgraph:
		return p.parseSubgraph(g)
	case TokenIdent, TokenString:
		return p.parseNodeOrEdgeOrAttr(g)
	case TokenSemicolon:
		p.advance()
		return nil
	default:
		return fmt.Errorf("line %d col %d: unexpected token %s in statement", tok.Line, tok.Col, tok)
	}
}

// parseGraphAttrStmt: 'graph' '[' Attr* ']'
func (p *parser) parseGraphAttrStmt(g *Graph) error {
	p.advance() // consume 'graph'
	if p.peek().Type == TokenLBracket {
		attrs, err := p.parseAttrBlock()
		if err != nil {
			return err
		}
		for k, v := range attrs {
			g.Attrs[k] = v
		}
	}
	return nil
}

// parseNodeDefaults: 'node' '[' Attr* ']'
func (p *parser) parseNodeDefaults() error {
	p.advance() // consume 'node'
	if p.peek().Type == TokenLBracket {
		attrs, err := p.parseAttrBlock()
		if err != nil {
			return err
		}
		for k, v := range attrs {
			p.nodeDefaults[k] = v
		}
	}
	return nil
}

// parseEdgeDefaults: 'edge' '[' Attr* ']'
func (p *parser) parseEdgeDefaults() error {
	p.advance() // consume 'edge'
	if p.peek().Type == TokenLBracket {
		attrs, err := p.parseAttrBlock()
		if err != nil {
			return err
		}
		for k, v := range attrs {
			p.edgeDefaults[k] = v
		}
	}
	return nil
}

// parseSubgraph: 'subgraph' Identifier? '{' Statement* '}'
// Flattens contents into the parent graph, applying scoped defaults.
func (p *parser) parseSubgraph(g *Graph) error {
	p.depth++
	if p.depth > maxSubgraphDepth {
		tok := p.peek()
		return fmt.Errorf("line %d col %d: subgraph nesting exceeds maximum depth (%d)", tok.Line, tok.Col, maxSubgraphDepth)
	}
	defer func() { p.depth-- }()

	p.advance() // consume 'subgraph'

	// Optional name
	if p.peek().Type == TokenIdent || p.peek().Type == TokenString {
		p.advance()
	}

	if _, err := p.expect(TokenLBrace); err != nil {
		return err
	}

	// Save and restore node/edge defaults for scoping.
	savedNodeDefaults := copyMap(p.nodeDefaults)
	savedEdgeDefaults := copyMap(p.edgeDefaults)

	if err := p.parseStatements(g); err != nil {
		return err
	}

	p.nodeDefaults = savedNodeDefaults
	p.edgeDefaults = savedEdgeDefaults

	if _, err := p.expect(TokenRBrace); err != nil {
		return err
	}
	return nil
}

// parseNodeOrEdgeOrAttr handles an identifier that could be the start of:
//   - A node declaration:     ID [attrs]
//   - An edge declaration:    ID -> ID ... [attrs]
//   - A graph attr decl:      key = value
func (p *parser) parseNodeOrEdgeOrAttr(g *Graph) error {
	idTok := p.advance()
	id := idTok.Val

	// Graph-level attribute: key = value
	if p.peek().Type == TokenEquals {
		p.advance() // consume '='
		valTok := p.advance()
		g.Attrs[id] = tokenValue(valTok)
		return nil
	}

	// Edge: ID -> ID [-> ID]* [attrs]
	if p.peek().Type == TokenArrow {
		return p.parseEdgeChain(g, id)
	}

	// Node: ID [attrs]
	return p.parseNodeStmt(g, id)
}

func (p *parser) parseNodeStmt(g *Graph, id string) error {
	attrs := copyMap(p.nodeDefaults)
	if p.peek().Type == TokenLBracket {
		explicit, err := p.parseAttrBlock()
		if err != nil {
			return err
		}
		for k, v := range explicit {
			attrs[k] = v
		}
	}
	// Only add the node if it doesn't already exist (edges may reference it first).
	if n := g.NodeByID(id); n != nil {
		for k, v := range attrs {
			n.Attrs[k] = v
		}
	} else {
		g.Nodes = append(g.Nodes, &Node{ID: id, Attrs: attrs})
	}
	return nil
}

// parseEdgeChain: ID ( '->' ID )+ [attrs]
// Chained edges share the same attr block applied to each pair.
func (p *parser) parseEdgeChain(g *Graph, firstID string) error {
	ids := []string{firstID}
	for p.peek().Type == TokenArrow {
		p.advance() // consume '->'
		tok := p.advance()
		if tok.Type != TokenIdent && tok.Type != TokenString {
			return fmt.Errorf("line %d col %d: expected node ID after '->', got %s", tok.Line, tok.Col, tok)
		}
		ids = append(ids, tok.Val)
	}

	// Optional attr block applies to all edges in the chain.
	attrs := copyMap(p.edgeDefaults)
	if p.peek().Type == TokenLBracket {
		explicit, err := p.parseAttrBlock()
		if err != nil {
			return err
		}
		for k, v := range explicit {
			attrs[k] = v
		}
	}

	for i := 0; i < len(ids)-1; i++ {
		edgeAttrs := copyMap(attrs)
		g.Edges = append(g.Edges, &Edge{From: ids[i], To: ids[i+1], Attrs: edgeAttrs})
		// Ensure nodes exist (edges can implicitly declare nodes).
		ensureNode(g, ids[i])
		ensureNode(g, ids[i+1])
	}

	return nil
}

// parseAttrBlock: '[' Attr ( ',' Attr )* ']'
func (p *parser) parseAttrBlock() (map[string]string, error) {
	if _, err := p.expect(TokenLBracket); err != nil {
		return nil, err
	}
	attrs := make(map[string]string)
	for p.peek().Type != TokenRBracket && p.peek().Type != TokenEOF {
		key, val, err := p.parseAttr()
		if err != nil {
			return nil, err
		}
		attrs[key] = val
		// Consume optional comma between attributes.
		if p.peek().Type == TokenComma {
			p.advance()
		}
	}
	if _, err := p.expect(TokenRBracket); err != nil {
		return nil, err
	}
	return attrs, nil
}

// parseAttr: Key '=' Value
// Key can be a qualified ID like "stack.child_dotfile".
func (p *parser) parseAttr() (string, string, error) {
	keyTok := p.advance()
	if keyTok.Type != TokenIdent && keyTok.Type != TokenString {
		return "", "", fmt.Errorf("line %d col %d: expected attribute key, got %s", keyTok.Line, keyTok.Col, keyTok)
	}
	key := keyTok.Val

	// Handle qualified IDs: ident.ident.ident
	for p.peek().Val == "." {
		// The '.' is lexed as part of a number if preceded by digits, but as a
		// separate character it won't be. We handle the qualified-id case by
		// checking if the next token after consuming looks like an ident.
		// Actually, '.' is not a standalone token in our lexer. Qualified IDs
		// in DOT attributes use underscores or are quoted strings. The spec
		// examples use "stack.child_dotfile" as quoted strings or bare idents
		// containing dots aren't valid per the BNF. We'll handle this in
		// attribute values as strings.
		break
	}

	if _, err := p.expect(TokenEquals); err != nil {
		return "", "", err
	}

	valTok := p.advance()
	return key, tokenValue(valTok), nil
}

// tokenValue extracts the string representation of a value token.
func tokenValue(tok Token) string {
	return tok.Val
}

func ensureNode(g *Graph, id string) {
	if g.NodeByID(id) == nil {
		g.Nodes = append(g.Nodes, &Node{ID: id, Attrs: make(map[string]string)})
	}
}

func copyMap(m map[string]string) map[string]string {
	c := make(map[string]string, len(m))
	for k, v := range m {
		c[k] = v
	}
	return c
}
