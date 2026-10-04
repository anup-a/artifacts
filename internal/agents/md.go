package agents

import (
	"html"
	"regexp"
	"strings"
)

// markdownToHTML renders the small Markdown subset the guides are written
// in: #/##/### headings, paragraphs, - and 1. lists, > quotes, pipe tables
// and fenced code, with inline `code`, **bold** and [links](url). The guides
// are our own text, so anything outside the subset is a bug in a guide, not
// input to defend against; every piece of text is still escaped.
func markdownToHTML(src string) string {
	var out strings.Builder
	lines := strings.Split(strings.ReplaceAll(src, "\r\n", "\n"), "\n")
	for i := 0; i < len(lines); {
		line := lines[i]
		trim := strings.TrimSpace(line)
		switch {
		case trim == "":
			i++
		case strings.HasPrefix(trim, "```"):
			lang := strings.TrimPrefix(trim, "```")
			i++
			var code []string
			for i < len(lines) && !strings.HasPrefix(strings.TrimSpace(lines[i]), "```") {
				code = append(code, lines[i])
				i++
			}
			i++ // the closing fence
			out.WriteString("<pre><code")
			if lang != "" {
				out.WriteString(` class="language-` + html.EscapeString(lang) + `"`)
			}
			out.WriteString(">" + html.EscapeString(strings.Join(code, "\n")) + "</code></pre>\n")
		case strings.HasPrefix(trim, "#"):
			level := len(trim) - len(strings.TrimLeft(trim, "#"))
			text := strings.TrimSpace(trim[level:])
			tag := "h" + string(rune('0'+level))
			out.WriteString("<" + tag + ` id="` + anchor(text) + `">` + inline(text) + "</" + tag + ">\n")
			i++
		case strings.HasPrefix(trim, "|"):
			var rows [][]string
			for i < len(lines) && strings.HasPrefix(strings.TrimSpace(lines[i]), "|") {
				rows = append(rows, cells(lines[i]))
				i++
			}
			out.WriteString(table(rows))
		case strings.HasPrefix(trim, "- "):
			out.WriteString("<ul>\n")
			for i < len(lines) && strings.HasPrefix(strings.TrimSpace(lines[i]), "- ") {
				out.WriteString("<li>" + inline(strings.TrimPrefix(strings.TrimSpace(lines[i]), "- ")) + "</li>\n")
				i++
			}
			out.WriteString("</ul>\n")
		case orderedItem.MatchString(trim):
			out.WriteString("<ol>\n")
			for i < len(lines) && orderedItem.MatchString(strings.TrimSpace(lines[i])) {
				out.WriteString("<li>" + inline(orderedItem.ReplaceAllString(strings.TrimSpace(lines[i]), "")) + "</li>\n")
				i++
			}
			out.WriteString("</ol>\n")
		case strings.HasPrefix(trim, ">"):
			var quote []string
			for i < len(lines) && strings.HasPrefix(strings.TrimSpace(lines[i]), ">") {
				quote = append(quote, strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(lines[i]), ">")))
				i++
			}
			out.WriteString("<blockquote><p>" + inline(strings.Join(quote, " ")) + "</p></blockquote>\n")
		default:
			var para []string
			for i < len(lines) && startsParagraph(lines[i]) {
				para = append(para, strings.TrimSpace(lines[i]))
				i++
			}
			out.WriteString("<p>" + inline(strings.Join(para, " ")) + "</p>\n")
		}
	}
	return out.String()
}

var (
	orderedItem = regexp.MustCompile(`^\d+\.\s+`)
	linkRe      = regexp.MustCompile(`\[([^\]]+)\]\(([^)\s]+)\)`)
	boldRe      = regexp.MustCompile(`\*\*([^*]+)\*\*`)
	bareURL     = regexp.MustCompile(`(^|[\s(])(https?://[^\s<>()]*[^\s<>().,;:])`)
	nonWord     = regexp.MustCompile(`[^a-z0-9]+`)
)

// startsParagraph reports whether a line continues a paragraph: anything
// that does not open another block.
func startsParagraph(line string) bool {
	t := strings.TrimSpace(line)
	if t == "" || orderedItem.MatchString(t) {
		return false
	}
	for _, p := range []string{"```", "#", "|", "- ", ">"} {
		if strings.HasPrefix(t, p) {
			return false
		}
	}
	return true
}

// inline escapes text and renders `code`, **bold**, [text](url) and bare
// http(s) URLs as links. Code
// spans are cut out first so their contents stay literal.
func inline(s string) string {
	var out strings.Builder
	parts := strings.Split(s, "`")
	for i, p := range parts {
		if i%2 == 1 && i < len(parts)-1 {
			out.WriteString("<code>" + html.EscapeString(p) + "</code>")
			continue
		}
		if i%2 == 1 { // an unmatched backtick
			out.WriteString("`")
		}
		t := html.EscapeString(p)
		t = linkRe.ReplaceAllString(t, `<a href="$2">$1</a>`)
		t = bareURL.ReplaceAllString(t, `$1<a href="$2">$2</a>`)
		t = boldRe.ReplaceAllString(t, `<strong>$1</strong>`)
		out.WriteString(t)
	}
	return out.String()
}

// anchor is a heading's id: lower case, words joined by hyphens.
func anchor(text string) string {
	return strings.Trim(nonWord.ReplaceAllString(strings.ToLower(text), "-"), "-")
}

func cells(line string) []string {
	t := strings.Trim(strings.TrimSpace(line), "|")
	var out []string
	for _, c := range strings.Split(t, "|") {
		out = append(out, strings.TrimSpace(c))
	}
	return out
}

// table renders a pipe table whose second row is the |---| separator.
func table(rows [][]string) string {
	var out strings.Builder
	out.WriteString("<div class=\"table\"><table>\n")
	for i, row := range rows {
		if i == 1 && strings.Trim(strings.Join(row, ""), "-: ") == "" {
			continue
		}
		cell := "td"
		if i == 0 {
			cell = "th"
			out.WriteString("<thead>")
		}
		out.WriteString("<tr>")
		for _, c := range row {
			out.WriteString("<" + cell + ">" + inline(c) + "</" + cell + ">")
		}
		out.WriteString("</tr>")
		if i == 0 {
			out.WriteString("</thead><tbody>")
		}
		out.WriteString("\n")
	}
	out.WriteString("</tbody></table></div>\n")
	return out.String()
}
