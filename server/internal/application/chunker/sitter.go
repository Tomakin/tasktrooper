package chunker

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	sitter "github.com/smacker/go-tree-sitter"

	"github.com/makifbaysal/tasktrooper/server/internal/application/treesitter"
	"github.com/makifbaysal/tasktrooper/server/internal/domain"
)

const (
	// symbolMaxLines bounds one symbol chunk; anything longer is split into
	// overlapping blocks so a 900-line component still embeds usefully.
	symbolMaxLines = 150
	symbolOverlap  = 20
	// gapMinLines is the shortest uncovered run worth its own chunk. Below it a
	// gap is imports, blank lines or a stray statement — noise to embed.
	gapMinLines = 25
	gapMaxLines = 200
	// maxSignatureChars keeps a signature to something a prompt can afford.
	maxSignatureChars = 240
)

// declRule says what one grammar node contributes to the chunk stream.
type declRule struct {
	// kind lands on Chunk.Kind ("function", "class", "method", ...).
	kind string
	// container means the node's members become their own chunks and the node
	// itself only contributes its header (declaration line plus fields).
	container bool
}

// sitterSpec adapts one tree-sitter grammar to the chunker. Grammars disagree
// on node names and on whether declarations carry a "name" field at all
// (Kotlin's do not), so everything that varies lives here and the walk below
// stays shared.
type sitterSpec struct {
	language string
	parse    func(path string, content []byte) *sitter.Tree
	// decls are the declarations chunked wherever the walk reaches them.
	decls map[string]declRule
	// members are the declarations chunked inside a container body. A node type
	// may appear in both maps with different kinds (a Kotlin top-level
	// `function_declaration` is a function, inside a class it is a method).
	members map[string]declRule
	// unwrap resolves a wrapper node (Python's decorated_definition, TypeScript's
	// export_statement) to the declaration it decorates. Returning nil means the
	// node is not a wrapper.
	unwrap func(node *sitter.Node) *sitter.Node
	// nameOf overrides name extraction for grammars the default cannot read.
	nameOf func(node *sitter.Node, content []byte) string
	// kindOf refines a rule's kind from the source, e.g. Swift's single
	// class_declaration node covering class/struct/enum/extension/actor.
	kindOf func(node *sitter.Node, content []byte, rule declRule) string
}

// chunkWithSitter parses one file and turns its declarations into chunks. It
// never fails the index: an unparsable file (or one the grammar yields nothing
// for) falls back to fixed-size chunks, which is what every non-Go file used to
// get unconditionally.
func chunkWithSitter(spec sitterSpec, filePath string, content []byte) ([]Chunk, error) {
	if strings.TrimSpace(string(content)) == "" {
		return nil, nil
	}
	tree := spec.parse(filePath, content)
	if tree == nil {
		return FallbackChunker{}.Chunk(filePath, content)
	}
	defer tree.Close()

	src := string(content)
	// One spec serves a whole family (.ts and .js share a chunker), so the
	// chunk's language comes from the file, not from the spec.
	language := languageFromExt(filepath.Ext(filePath))
	if language == "unknown" {
		language = spec.language
	}
	spec.language = language
	collector := &sitterCollector{spec: spec, filePath: filePath, content: content, src: src}
	collector.walk(tree.RootNode(), spec.decls, "")

	if len(collector.chunks) == 0 {
		return FallbackChunker{}.Chunk(filePath, content)
	}

	// Top-level statements, big literals and anything the grammar could not
	// parse live between the declarations. Without this a 700-line route file
	// whose payload is one template literal would index only its two functions.
	chunks := append(collector.chunks, collector.gapChunks()...)
	sort.SliceStable(chunks, func(i, j int) bool { return chunks[i].StartLine < chunks[j].StartLine })
	return chunks, nil
}

type sitterCollector struct {
	spec     sitterSpec
	filePath string
	content  []byte
	src      string
	chunks   []Chunk
	// covered records the line ranges declarations already account for.
	covered [][2]int
}

func (c *sitterCollector) walk(node *sitter.Node, rules map[string]declRule, prefix string) {
	if node == nil {
		return
	}
	target := node
	if c.spec.unwrap != nil {
		if inner := c.spec.unwrap(node); inner != nil {
			// Keep the wrapper's span so decorators and `export` stay in the
			// chunk, but classify by the declaration inside it.
			target = inner
		}
	}
	if rule, ok := rules[target.Type()]; ok {
		c.emit(node, target, rule, prefix)
		return
	}
	for i := 0; i < int(node.NamedChildCount()); i++ {
		c.walk(node.NamedChild(i), rules, prefix)
	}
}

func (c *sitterCollector) emit(outer, node *sitter.Node, rule declRule, prefix string) {
	name := c.symbolName(node)
	if name == "" {
		name = filepath.Base(c.filePath)
	}
	if prefix != "" {
		name = prefix + "." + name
	}
	kind := rule.kind
	if c.spec.kindOf != nil {
		kind = c.spec.kindOf(node, c.content, rule)
	}
	signature := c.signature(node)

	startLine := int(outer.StartPoint().Row) + 1
	endLine := int(node.EndPoint().Row) + 1
	c.markCovered(startLine, endLine)

	if !rule.container {
		c.chunks = append(c.chunks, c.sized(kind, name, signature, startLine, endLine)...)
		return
	}

	members := c.memberNodes(node)
	if len(members) == 0 {
		c.chunks = append(c.chunks, c.sized(kind, name, signature, startLine, endLine)...)
		return
	}

	// The container header — declaration line, annotations, fields — stops
	// where its first member starts, so a class and its methods never embed the
	// same lines twice.
	headerEnd := int(members[0].StartPoint().Row)
	if headerEnd < startLine {
		// A one-line declaration (`protocol Loader { func load() }`) leaves no
		// room for a header; keep the declaration line so the type itself stays
		// a searchable symbol.
		headerEnd = startLine
	}
	c.chunks = append(c.chunks, c.sized(kind, name, signature, startLine, headerEnd)...)
	for _, member := range members {
		c.walk(member, c.spec.members, name)
	}
}

// memberNodes returns the declarations directly inside a container's body, in
// source order.
func (c *sitterCollector) memberNodes(node *sitter.Node) []*sitter.Node {
	body := c.bodyNode(node)
	if body == nil {
		return nil
	}
	var members []*sitter.Node
	for i := 0; i < int(body.NamedChildCount()); i++ {
		child := body.NamedChild(i)
		target := child
		if c.spec.unwrap != nil {
			if inner := c.spec.unwrap(child); inner != nil {
				target = inner
			}
		}
		if _, ok := c.spec.members[target.Type()]; ok {
			members = append(members, child)
		}
	}
	return members
}

// bodyNode finds the block a declaration encloses. Grammars either name the
// field ("body") or leave it as a typed child (Kotlin's class_body).
func (c *sitterCollector) bodyNode(node *sitter.Node) *sitter.Node {
	if body := node.ChildByFieldName("body"); body != nil {
		return body
	}
	for i := 0; i < int(node.NamedChildCount()); i++ {
		child := node.NamedChild(i)
		if strings.HasSuffix(child.Type(), "_body") || child.Type() == "block" {
			return child
		}
	}
	return nil
}

func (c *sitterCollector) symbolName(node *sitter.Node) string {
	if c.spec.nameOf != nil {
		if name := c.spec.nameOf(node, c.content); name != "" {
			return name
		}
	}
	if nameNode := node.ChildByFieldName("name"); nameNode != nil {
		return cleanSymbolName(treesitter.NodeText(nameNode, c.content))
	}
	// Kotlin and friends carry no name field: the first identifier-shaped child
	// of the declaration is the name.
	if name := firstIdentifier(node, c.content, 2); name != "" {
		return name
	}
	return ""
}

func (c *sitterCollector) signature(node *sitter.Node) string {
	body := c.bodyNode(node)
	end := node.EndByte()
	if body != nil {
		end = body.StartByte()
	}
	if end > uint32(len(c.content)) {
		end = uint32(len(c.content))
	}
	text := strings.TrimSpace(string(c.content[node.StartByte():end]))
	text = strings.Join(strings.Fields(text), " ")
	text = strings.TrimRight(text, "{(=")
	text = strings.TrimSpace(text)
	if len(text) > maxSignatureChars {
		// Cut on a rune boundary: half a multi-byte character is invalid
		// UTF-8, and Postgres rejects the symbol row and the whole index run.
		text = domain.TruncateHead(text, maxSignatureChars) + "…"
	}
	return text
}

// sized emits one chunk per symbol, splitting oversized bodies the way the Go
// chunker does so a single huge function cannot dominate an embedding.
func (c *sitterCollector) sized(kind, name, signature string, startLine, endLine int) []Chunk {
	if endLine < startLine {
		return nil
	}
	if endLine-startLine+1 <= symbolMaxLines {
		content := extractLines(c.src, startLine, endLine)
		if strings.TrimSpace(content) == "" {
			return nil
		}
		return []Chunk{{
			FilePath:   c.filePath,
			Language:   c.spec.language,
			Kind:       kind,
			SymbolName: name,
			StartLine:  startLine,
			EndLine:    endLine,
			Content:    content,
			Signature:  signature,
		}}
	}

	var chunks []Chunk
	for start := startLine; start <= endLine; {
		end := start + symbolMaxLines - 1
		if end > endLine {
			end = endLine
		}
		chunks = append(chunks, Chunk{
			FilePath:   c.filePath,
			Language:   c.spec.language,
			Kind:       "block",
			SymbolName: fmt.Sprintf("%s.block%d", name, len(chunks)+1),
			StartLine:  start,
			EndLine:    end,
			Content:    extractLines(c.src, start, end),
			Signature:  signature,
		})
		if end >= endLine {
			break
		}
		next := end - symbolOverlap + 1
		if next <= start {
			next = end + 1
		}
		start = next
	}
	return chunks
}

func (c *sitterCollector) markCovered(startLine, endLine int) {
	if endLine < startLine {
		return
	}
	c.covered = append(c.covered, [2]int{startLine, endLine})
}

// gapChunks covers what no declaration claimed: module-level configuration,
// big literals, JSX at file scope, and regions the grammar failed on.
func (c *sitterCollector) gapChunks() []Chunk {
	total := len(strings.Split(c.src, "\n"))
	sort.Slice(c.covered, func(i, j int) bool { return c.covered[i][0] < c.covered[j][0] })

	var chunks []Chunk
	cursor := 1
	flush := func(start, end int) {
		if end < start || end-start+1 < gapMinLines {
			return
		}
		if countNonBlank(c.src, start, end) < gapMinLines/2 {
			return
		}
		for s := start; s <= end; {
			e := s + gapMaxLines - 1
			if e > end {
				e = end
			}
			chunks = append(chunks, Chunk{
				FilePath:   c.filePath,
				Language:   c.spec.language,
				Kind:       "block",
				SymbolName: fmt.Sprintf("%s.top%d", filepath.Base(c.filePath), len(chunks)+1),
				StartLine:  s,
				EndLine:    e,
				Content:    extractLines(c.src, s, e),
			})
			s = e + 1
		}
	}
	for _, span := range c.covered {
		if span[0] > cursor {
			flush(cursor, span[0]-1)
		}
		if span[1]+1 > cursor {
			cursor = span[1] + 1
		}
	}
	flush(cursor, total)
	return chunks
}

func countNonBlank(src string, startLine, endLine int) int {
	lines := strings.Split(src, "\n")
	count := 0
	for i := startLine - 1; i < endLine && i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) != "" {
			count++
		}
	}
	return count
}

// identifierTypes are the node types that carry a declaration's name across the
// grammars wired up here.
var identifierTypes = map[string]struct{}{
	"identifier":          {},
	"simple_identifier":   {},
	"type_identifier":     {},
	"property_identifier": {},
	"bound_identifier":    {},
}

func firstIdentifier(node *sitter.Node, content []byte, depth int) string {
	if node == nil || depth < 0 {
		return ""
	}
	for i := 0; i < int(node.NamedChildCount()); i++ {
		child := node.NamedChild(i)
		if _, ok := identifierTypes[child.Type()]; ok {
			return cleanSymbolName(treesitter.NodeText(child, content))
		}
	}
	for i := 0; i < int(node.NamedChildCount()); i++ {
		child := node.NamedChild(i)
		// Modifiers and annotations precede the name; keep descending past them.
		if name := firstIdentifier(child, content, depth-1); name != "" {
			return name
		}
	}
	return ""
}

// leadingKeyword reports the first bare keyword token of a declaration, which
// is how Swift and Kotlin distinguish a struct from a class from an extension
// when the grammar gives them all one node type.
func leadingKeyword(node *sitter.Node, content []byte, keywords map[string]string) string {
	if node == nil {
		return ""
	}
	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		if child.IsNamed() {
			continue
		}
		if kind, ok := keywords[treesitter.NodeText(child, content)]; ok {
			return kind
		}
	}
	return ""
}

func cleanSymbolName(name string) string {
	name = strings.TrimSpace(name)
	name = strings.Trim(name, "`\"'")
	if idx := strings.IndexAny(name, "\n<("); idx > 0 {
		name = name[:idx]
	}
	return strings.TrimSpace(name)
}
