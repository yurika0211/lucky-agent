package tool

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"html"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

const maxDocumentReadBytes = 50 * 1024 * 1024

func DocumentReadTool() *Tool {
	return &Tool{
		Name:         "document_read",
		Description:  "Extract readable text from local document files such as PDF, DOCX, and PPTX. Use this instead of file_read for Office documents or PDFs.",
		Category:     CatBuiltin,
		Source:       "builtin",
		Permission:   PermAuto,
		ShellAware:   true,
		ParallelSafe: true,
		Parameters: map[string]Param{
			"path":   {Type: "string", Description: "Path to a local .pdf, .docx, or .pptx file.", Required: true},
			"offset": {Type: "number", Description: "Line number to start reading extracted text from (1-indexed)", Required: false, Default: 1},
			"limit":  {Type: "number", Description: "Maximum number of extracted text lines to return", Required: false, Default: 2000},
		},
		Handler: handleDocumentRead,
	}
}

func handleDocumentRead(args map[string]any) (string, error) {
	path, err := resolvePathArg(args, "path")
	if err != nil {
		return "", err
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("stat document: %w", err)
	}
	if info.IsDir() {
		return "", fmt.Errorf("document_read expects a file, got directory: %s", path)
	}
	if info.Size() > maxDocumentReadBytes {
		return "", fmt.Errorf("document is too large (%d bytes, max %d)", info.Size(), maxDocumentReadBytes)
	}

	text, format, err := ExtractDocumentText(path)
	if err != nil {
		return "", err
	}
	text = normalizeExtractedDocumentText(text)
	if text == "" {
		return "", fmt.Errorf("no extractable text found in %s document: %s", format, path)
	}

	offset := intArg(args, "offset", 1)
	if offset < 1 {
		offset = 1
	}
	limit := intArg(args, "limit", 2000)
	if limit <= 0 {
		limit = 2000
	}
	return formatDocumentReadOutput(path, format, text, offset, limit)
}

func ExtractDocumentText(path string) (text, format string, err error) {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".docx":
		text, err = extractDocxText(path)
		return text, "docx", err
	case ".pptx":
		text, err = extractPptxText(path)
		return text, "pptx", err
	case ".pdf":
		text, err = extractPDFText(path)
		return text, "pdf", err
	case ".doc":
		return "", "doc", fmt.Errorf("legacy .doc files are not supported; convert to .docx first: %s", path)
	case ".ppt":
		return "", "ppt", fmt.Errorf("legacy .ppt files are not supported; convert to .pptx first: %s", path)
	default:
		return "", "", fmt.Errorf("unsupported document format %q; supported formats: .pdf, .docx, .pptx", filepath.Ext(path))
	}
}

func extractDocxText(path string) (string, error) {
	zr, err := zip.OpenReader(path)
	if err != nil {
		return "", fmt.Errorf("open docx: %w", err)
	}
	defer zr.Close()

	var parts []string
	for _, name := range []string{"word/document.xml", "word/footnotes.xml", "word/endnotes.xml"} {
		if text, err := extractXMLTextFromZip(&zr.Reader, name); err == nil && strings.TrimSpace(text) != "" {
			parts = append(parts, text)
		}
	}
	if len(parts) == 0 {
		return "", nil
	}
	return strings.Join(parts, "\n"), nil
}

func extractPptxText(path string) (string, error) {
	zr, err := zip.OpenReader(path)
	if err != nil {
		return "", fmt.Errorf("open pptx: %w", err)
	}
	defer zr.Close()

	var slides []string
	for _, f := range zr.File {
		name := filepath.ToSlash(f.Name)
		if strings.HasPrefix(name, "ppt/slides/slide") && strings.HasSuffix(name, ".xml") {
			slides = append(slides, name)
		}
	}
	sort.Slice(slides, func(i, j int) bool {
		return slideNumber(slides[i]) < slideNumber(slides[j])
	})

	var parts []string
	for _, name := range slides {
		text, err := extractXMLTextFromZip(&zr.Reader, name)
		if err != nil || strings.TrimSpace(text) == "" {
			continue
		}
		parts = append(parts, fmt.Sprintf("[Slide %d]\n%s", slideNumber(name), text))
	}
	return strings.Join(parts, "\n\n"), nil
}

func extractPDFText(path string) (string, error) {
	bin, err := exec.LookPath("pdftotext")
	if err != nil {
		return "", fmt.Errorf("pdf text extraction requires pdftotext; install poppler-utils or convert the PDF to text first")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "-layout", "-enc", "UTF-8", path, "-")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if ctx.Err() == context.DeadlineExceeded {
		return "", fmt.Errorf("pdftotext timed out after 30 seconds")
	}
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("pdftotext failed: %s", msg)
	}
	return string(out), nil
}

func extractXMLTextFromZip(zr *zip.Reader, name string) (string, error) {
	for _, f := range zr.File {
		if filepath.ToSlash(f.Name) != name {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return "", err
		}
		defer rc.Close()
		return extractTextFromOfficeXML(rc)
	}
	return "", fmt.Errorf("zip member not found: %s", name)
}

func extractTextFromOfficeXML(r io.Reader) (string, error) {
	decoder := xml.NewDecoder(r)
	var b strings.Builder
	needsSpace := false
	for {
		tok, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "p", "br", "tab", "tr":
				writeDocumentBreak(&b)
				needsSpace = false
			}
		case xml.CharData:
			text := strings.TrimSpace(html.UnescapeString(string(t)))
			if text == "" {
				continue
			}
			if needsSpace && b.Len() > 0 {
				b.WriteByte(' ')
			}
			b.WriteString(text)
			needsSpace = true
		}
	}
	return b.String(), nil
}

func writeDocumentBreak(b *strings.Builder) {
	if b.Len() == 0 {
		return
	}
	s := b.String()
	if strings.HasSuffix(s, "\n") {
		return
	}
	b.WriteByte('\n')
}

func normalizeExtractedDocumentText(text string) string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	lines := strings.Split(text, "\n")
	out := make([]string, 0, len(lines))
	blank := false
	for _, line := range lines {
		line = strings.Join(strings.Fields(line), " ")
		if line == "" {
			if blank {
				continue
			}
			blank = true
			out = append(out, "")
			continue
		}
		blank = false
		out = append(out, line)
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}

func formatDocumentReadOutput(path, format, text string, offset, limit int) (string, error) {
	lines := strings.Split(text, "\n")
	start := offset - 1
	if start >= len(lines) {
		return "", fmt.Errorf("offset %d exceeds extracted text line count %d", offset, len(lines))
	}
	end := start + limit
	truncated := false
	if end < len(lines) {
		truncated = true
	} else {
		end = len(lines)
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf("Document: %s\n", path))
	b.WriteString(fmt.Sprintf("Format: %s\n", format))
	b.WriteString(fmt.Sprintf("Lines: %d", len(lines)))
	if truncated {
		b.WriteString(fmt.Sprintf(" (showing %d-%d)", start+1, end))
	}
	b.WriteString("\n\n")
	for i := start; i < end; i++ {
		b.WriteString(fmt.Sprintf("%d| %s\n", i+1, lines[i]))
	}
	if truncated {
		b.WriteString(fmt.Sprintf("... truncated; use offset=%d to continue\n", end+1))
	}
	return b.String(), nil
}

func intArg(args map[string]any, key string, def int) int {
	raw, ok := args[key]
	if !ok {
		return def
	}
	switch v := raw.(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	case string:
		if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
			return n
		}
	}
	return def
}

func slideNumber(name string) int {
	base := filepath.Base(name)
	base = strings.TrimSuffix(strings.TrimPrefix(base, "slide"), ".xml")
	n, err := strconv.Atoi(base)
	if err != nil {
		return 0
	}
	return n
}
