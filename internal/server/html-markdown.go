package server

import (
	"fmt"
	"io"
	"net/url"
	"regexp"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// htmlToMarkdown converts an HTML document to Markdown for a model to read.
// It keeps structure (headings, lists, links, code, tables, quotes) and drops
// what carries no content: scripts, styles, forms, embedded media and
// navigation chrome. base resolves relative links. The title, when present,
// becomes the first heading.
func htmlToMarkdown(r io.Reader, base *url.URL) (string, error) {
	doc, err := html.Parse(r)
	if err != nil {
		return "", fmt.Errorf("parse html: %w", err)
	}
	c := &mdConverter{base: base}
	if title := strings.TrimSpace(nodeText(findElement(doc, atom.Title))); title != "" {
		c.title = collapseSpace(title)
	}
	root := findElement(doc, atom.Main)
	if root == nil {
		root = findElement(doc, atom.Article)
	}
	if root == nil {
		root = findElement(doc, atom.Body)
	}
	if root == nil {
		root = doc
	}
	c.block(root)
	out := c.finish()
	if c.title != "" && !strings.HasPrefix(out, "# ") {
		out = "# " + c.title + "\n\n" + out
	}
	return out, nil
}

// htmlToText is htmlToMarkdown without Markdown syntax.
func htmlToText(r io.Reader) (string, error) {
	doc, err := html.Parse(r)
	if err != nil {
		return "", fmt.Errorf("parse html: %w", err)
	}
	root := findElement(doc, atom.Body)
	if root == nil {
		root = doc
	}
	c := &mdConverter{plain: true}
	c.block(root)
	return c.finish(), nil
}

type mdConverter struct {
	base  *url.URL
	title string
	plain bool
	out   strings.Builder
	line  strings.Builder
	// prefix is prepended to every line (blockquotes, list continuation).
	prefix []string
	lists  []mdList
	inPre  bool
}

type mdList struct {
	ordered bool
	n       int
}

var skippedElements = map[atom.Atom]bool{
	atom.Script: true, atom.Style: true, atom.Noscript: true, atom.Template: true,
	atom.Svg: true, atom.Iframe: true, atom.Object: true, atom.Embed: true,
	atom.Canvas: true, atom.Video: true, atom.Audio: true, atom.Form: true,
	atom.Button: true, atom.Select: true, atom.Input: true, atom.Textarea: true,
	atom.Nav: true, atom.Footer: true, atom.Head: true, atom.Dialog: true,
}

var blockElements = map[atom.Atom]bool{
	atom.P: true, atom.Div: true, atom.Section: true, atom.Article: true, atom.Main: true,
	atom.Header: true, atom.Aside: true, atom.Figure: true, atom.Figcaption: true,
	atom.Address: true, atom.Details: true, atom.Summary: true, atom.Dl: true,
	atom.Dt: true, atom.Dd: true, atom.Body: true, atom.Html: true, atom.Center: true,
}

func (c *mdConverter) block(n *html.Node) {
	for child := n.FirstChild; child != nil; child = child.NextSibling {
		c.node(child)
	}
}

func (c *mdConverter) node(n *html.Node) {
	switch n.Type {
	case html.TextNode:
		c.text(n.Data)
		return
	case html.ElementNode:
	default:
		c.block(n)
		return
	}
	if skippedElements[n.DataAtom] || hiddenElement(n) {
		return
	}
	switch n.DataAtom {
	case atom.H1, atom.H2, atom.H3, atom.H4, atom.H5, atom.H6:
		text := collapseSpace(c.inline(n))
		if text == "" {
			return
		}
		c.paragraph()
		if c.plain {
			c.write(text)
		} else {
			c.write(strings.Repeat("#", int(n.Data[1]-'0')) + " " + text)
		}
		c.paragraph()
	case atom.Br:
		c.flushLine()
	case atom.Hr:
		c.paragraph()
		if !c.plain {
			c.write("---")
		}
		c.paragraph()
	case atom.Pre:
		c.paragraph()
		code := nodeText(n)
		lang := ""
		if codeEl := findElement(n, atom.Code); codeEl != nil {
			for _, cls := range strings.Fields(attr(codeEl, "class")) {
				if l, ok := strings.CutPrefix(cls, "language-"); ok {
					lang = l
				}
			}
		}
		code = strings.Trim(code, "\n")
		if c.plain {
			for _, l := range strings.Split(code, "\n") {
				c.write(l)
				c.flushLine()
			}
		} else {
			fence := "```"
			for strings.Contains(code, fence) {
				fence += "`"
			}
			c.write(fence + lang)
			c.flushLine()
			for _, l := range strings.Split(code, "\n") {
				c.line.WriteString(l)
				c.flushLine()
			}
			c.write(fence)
		}
		c.paragraph()
	case atom.Blockquote:
		c.paragraph()
		if !c.plain {
			c.prefix = append(c.prefix, "> ")
		}
		c.block(n)
		c.paragraph()
		if !c.plain {
			c.prefix = c.prefix[:len(c.prefix)-1]
		}
	case atom.Ul, atom.Ol:
		if len(c.lists) == 0 {
			c.paragraph()
		} else {
			c.flushLine()
		}
		c.lists = append(c.lists, mdList{ordered: n.DataAtom == atom.Ol})
		c.block(n)
		c.lists = c.lists[:len(c.lists)-1]
		if len(c.lists) == 0 {
			c.paragraph()
		} else {
			c.flushLine()
		}
	case atom.Li:
		c.flushLine()
		depth := len(c.lists)
		marker := "- "
		if depth > 0 {
			l := &c.lists[depth-1]
			l.n++
			if l.ordered {
				marker = fmt.Sprintf("%d. ", l.n)
			}
		}
		// The enclosing item's prefix already indents nested lists.
		c.write(marker)
		c.prefix = append(c.prefix, strings.Repeat(" ", len(marker)))
		c.block(n)
		c.flushLine()
		c.prefix = c.prefix[:len(c.prefix)-1]
	case atom.Table:
		c.paragraph()
		c.table(n)
		c.paragraph()
	case atom.Img:
		alt := collapseSpace(attr(n, "alt"))
		if alt != "" {
			if c.plain {
				c.text(alt)
			} else {
				c.raw(fmt.Sprintf("![%s](%s)", alt, c.resolve(attr(n, "src"))))
			}
		}
	case atom.A, atom.Strong, atom.B, atom.Em, atom.I, atom.Code, atom.Del, atom.S:
		c.raw(c.inlineElement(n))
	default:
		if blockElements[n.DataAtom] {
			c.paragraph()
			c.block(n)
			c.paragraph()
			return
		}
		c.block(n)
	}
}

// inline renders an element's children as one line of Markdown.
func (c *mdConverter) inline(n *html.Node) string {
	var b strings.Builder
	for child := n.FirstChild; child != nil; child = child.NextSibling {
		switch child.Type {
		case html.TextNode:
			b.WriteString(child.Data)
		case html.ElementNode:
			if skippedElements[child.DataAtom] || hiddenElement(child) {
				continue
			}
			switch child.DataAtom {
			case atom.Br:
				b.WriteString(" ")
			case atom.Img:
				if alt := collapseSpace(attr(child, "alt")); alt != "" {
					b.WriteString(alt)
				}
			default:
				b.WriteString(c.inlineElement(child))
			}
		}
	}
	return b.String()
}

func (c *mdConverter) inlineElement(n *html.Node) string {
	switch n.DataAtom {
	case atom.Code:
		text := nodeText(n)
		if c.plain || text == "" {
			return text
		}
		tick := "`"
		for strings.Contains(text, tick) {
			tick += "`"
		}
		return tick + text + tick
	}
	text := collapseSpace(c.inline(n))
	if text == "" || c.plain {
		return text
	}
	switch n.DataAtom {
	case atom.A:
		href := attr(n, "href")
		if href == "" || strings.HasPrefix(href, "#") || strings.HasPrefix(strings.ToLower(href), "javascript:") {
			return text
		}
		return "[" + strings.ReplaceAll(text, "]", "\\]") + "](" + c.resolve(href) + ")"
	case atom.Strong, atom.B:
		return "**" + text + "**"
	case atom.Em, atom.I:
		return "*" + text + "*"
	case atom.Del, atom.S:
		return "~~" + text + "~~"
	}
	return text
}

func (c *mdConverter) table(n *html.Node) {
	var rows [][]string
	var walk func(*html.Node)
	walk = func(node *html.Node) {
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			if child.Type != html.ElementNode {
				continue
			}
			if child.DataAtom == atom.Tr {
				var cells []string
				for cell := child.FirstChild; cell != nil; cell = cell.NextSibling {
					if cell.Type == html.ElementNode && (cell.DataAtom == atom.Td || cell.DataAtom == atom.Th) {
						cells = append(cells, strings.ReplaceAll(collapseSpace(c.inline(cell)), "|", "\\|"))
					}
				}
				if len(cells) > 0 {
					rows = append(rows, cells)
				}
				continue
			}
			if child.DataAtom == atom.Table {
				continue
			}
			walk(child)
		}
	}
	walk(n)
	if len(rows) == 0 {
		return
	}
	width := 0
	for _, r := range rows {
		width = max(width, len(r))
	}
	for i, r := range rows {
		for len(r) < width {
			r = append(r, "")
		}
		if c.plain {
			c.write(strings.Join(r, "\t"))
		} else {
			c.write("| " + strings.Join(r, " | ") + " |")
			if i == 0 {
				c.flushLine()
				c.write("|" + strings.Repeat(" --- |", width))
			}
		}
		c.flushLine()
	}
}

func (c *mdConverter) resolve(href string) string {
	href = strings.TrimSpace(href)
	if c.base == nil || href == "" {
		return href
	}
	u, err := url.Parse(href)
	if err != nil {
		return href
	}
	return c.base.ResolveReference(u).String()
}

var spaceRun = regexp.MustCompile(`\s+`)

func collapseSpace(s string) string { return strings.TrimSpace(spaceRun.ReplaceAllString(s, " ")) }

// text appends running text, collapsing whitespace as a browser would.
func (c *mdConverter) text(s string) {
	s = spaceRun.ReplaceAllString(s, " ")
	if s == " " || s == "" {
		if c.line.Len() > 0 && !strings.HasSuffix(c.line.String(), " ") {
			c.line.WriteString(" ")
		}
		return
	}
	if c.line.Len() == 0 || strings.HasSuffix(c.line.String(), " ") {
		s = strings.TrimLeft(s, " ")
	}
	c.raw(s)
}

func (c *mdConverter) raw(s string) {
	if c.line.Len() == 0 {
		c.line.WriteString(strings.Join(c.prefix, ""))
	}
	c.line.WriteString(s)
}

func (c *mdConverter) write(s string) {
	c.raw(s)
}

func (c *mdConverter) flushLine() {
	line := strings.TrimRight(c.line.String(), " ")
	c.line.Reset()
	if strings.TrimSpace(line) == "" {
		return
	}
	c.out.WriteString(line)
	c.out.WriteString("\n")
}

func (c *mdConverter) paragraph() {
	c.flushLine()
	if c.out.Len() > 0 && !strings.HasSuffix(c.out.String(), "\n\n") {
		c.out.WriteString("\n")
	}
}

func (c *mdConverter) finish() string {
	c.flushLine()
	out := regexp.MustCompile(`\n{3,}`).ReplaceAllString(c.out.String(), "\n\n")
	return strings.TrimSpace(out) + "\n"
}

func hiddenElement(n *html.Node) bool {
	if _, ok := attrValue(n, "hidden"); ok {
		return true
	}
	if attr(n, "aria-hidden") == "true" {
		return true
	}
	style := strings.ReplaceAll(strings.ToLower(attr(n, "style")), " ", "")
	return strings.Contains(style, "display:none") || strings.Contains(style, "visibility:hidden")
}

func attr(n *html.Node, key string) string {
	v, _ := attrValue(n, key)
	return v
}

func attrValue(n *html.Node, key string) (string, bool) {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val, true
		}
	}
	return "", false
}

func findElement(n *html.Node, a atom.Atom) *html.Node {
	if n.Type == html.ElementNode && n.DataAtom == a {
		return n
	}
	for child := n.FirstChild; child != nil; child = child.NextSibling {
		if found := findElement(child, a); found != nil {
			return found
		}
	}
	return nil
}

func nodeText(n *html.Node) string {
	if n == nil {
		return ""
	}
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(node *html.Node) {
		if node.Type == html.TextNode {
			b.WriteString(node.Data)
		}
		if node.Type == html.ElementNode && node.DataAtom == atom.Br {
			b.WriteString("\n")
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(n)
	return b.String()
}
