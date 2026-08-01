package tool

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	_ "github.com/mattn/go-sqlite3"
	"github.com/yurika0211/luckyagent/internal/utils"
	"gopkg.in/yaml.v3"
)

const (
	defaultLogTailMaxBytes   = 64 << 10
	maxLogTailBytes          = 1 << 20
	logTailReadBlockBytes    = 32 << 10
	defaultLogGrepOutputByte = 64 << 10
	maxLogGrepOutputBytes    = 1 << 20
	defaultLogGrepScanLines  = 1_000_000
	maxLogGrepScanLines      = 10_000_000
	maxLogScannerTokenBytes  = 10 << 20
	defaultStructuredMaxFile = 20 << 20
	maxStructuredFileBytes   = 100 << 20
	defaultStructuredOutput  = 12000
	maxStructuredOutput      = 1 << 20
	maxStructuredQueryChars  = 2048
	defaultCSVMaxScanRows    = 100_000
	maxCSVMaxScanRows        = 1_000_000
	defaultSQLTimeoutSeconds = 10
	maxSQLTimeoutSeconds     = 60
	defaultDBSchemaTableMax  = 100
	maxDBSchemaTableMax      = 1000
	maxHTTPRequestBodyBytes  = 1 << 20
	defaultHTTPResponseBytes = 32 << 10
	maxHTTPResponseBytes     = 1 << 20
)

// LogTailTool returns the last N lines of a log file.
func LogTailTool() *Tool {
	return &Tool{
		Name:        "log_tail",
		Description: "Read the tail of a local log file. Use this when debugging runtime failures, service errors, or recent events near the end of a log.",
		Category:    CatBuiltin,
		Source:      "builtin",
		Permission:  PermAuto,
		Parameters: map[string]Param{
			"path":              {Type: "string", Description: "Path to the log file.", Required: true},
			"lines":             {Type: "number", Description: "Number of trailing lines to return (default 100, max 500).", Required: false, Default: 100},
			"max_bytes":         {Type: "number", Description: "Maximum bytes to read from the file tail (default 65536, max 1048576).", Required: false, Default: defaultLogTailMaxBytes},
			"with_line_numbers": {Type: "boolean", Description: "Prefix returned lines with original 1-based line numbers.", Required: false, Default: false},
			"include_meta":      {Type: "boolean", Description: "Return JSON with file size, returned line count, and truncation metadata.", Required: false, Default: false},
		},
		Handler:      handleLogTail,
		ParallelSafe: true,
	}
}

func handleLogTail(args map[string]any) (string, error) {
	path, ok := args["path"].(string)
	if !ok {
		return "", fmt.Errorf("path is required")
	}
	if err := validatePath(path); err != nil {
		return "", err
	}
	lines := boundedIntArg(args, "lines", 100, 1, 500)
	maxBytes := boundedIntArg(args, "max_bytes", defaultLogTailMaxBytes, 1024, maxLogTailBytes)
	withLineNumbers, _ := args["with_line_numbers"].(bool)
	includeMeta, _ := args["include_meta"].(bool)

	tail, meta, err := readLogTail(path, lines, maxBytes)
	if err != nil {
		return "", err
	}

	startLine := 0
	if withLineNumbers {
		totalLines, err := countTextFileLines(path)
		if err != nil {
			return "", err
		}
		startLine = maxInt(1, totalLines-len(tail)+1)
	}

	if includeMeta {
		result := map[string]any{
			"meta": meta,
		}
		if withLineNumbers {
			numbered := make([]map[string]any, 0, len(tail))
			for i, line := range tail {
				numbered = append(numbered, map[string]any{
					"line": line,
					"no":   startLine + i,
				})
			}
			result["lines"] = numbered
		} else {
			result["lines"] = tail
		}
		return prettyStructuredValue(result)
	}

	if withLineNumbers {
		numbered := make([]string, 0, len(tail))
		for i, line := range tail {
			numbered = append(numbered, fmt.Sprintf("%d| %s", startLine+i, line))
		}
		return strings.Join(numbered, "\n"), nil
	}
	return strings.Join(tail, "\n"), nil
}

// LogGrepTool searches a log file and returns matching lines with context.
func LogGrepTool() *Tool {
	return &Tool{
		Name:        "log_grep",
		Description: "Search a local log file for a string or regex and return surrounding context. Use this to locate stack traces, error bursts, or request IDs.",
		Category:    CatBuiltin,
		Source:      "builtin",
		Permission:  PermAuto,
		Parameters: map[string]Param{
			"path":        {Type: "string", Description: "Path to the log file.", Required: true},
			"pattern":     {Type: "string", Description: "Substring or regular expression to search for.", Required: true},
			"regex":       {Type: "boolean", Description: "Treat pattern as a regular expression.", Required: false, Default: false},
			"before":      {Type: "number", Description: "Lines of context before each match (default 2).", Required: false, Default: 2},
			"after":       {Type: "number", Description: "Lines of context after each match (default 2).", Required: false, Default: 2},
			"max_matches": {Type: "number", Description: "Maximum number of matches to return (default 20).", Required: false, Default: 20},
			"ignore_case": {Type: "boolean", Description: "Match case-insensitively.", Required: false, Default: false},
			"max_scan_lines": {
				Type:        "number",
				Description: "Maximum number of lines to scan (default 1000000, max 10000000).",
				Required:    false,
				Default:     defaultLogGrepScanLines,
			},
			"max_output_bytes": {
				Type:        "number",
				Description: "Maximum output bytes (default 65536, max 1048576).",
				Required:    false,
				Default:     defaultLogGrepOutputByte,
			},
			"include_meta": {Type: "boolean", Description: "Return JSON with output and scan metadata.", Required: false, Default: false},
		},
		Handler:      handleLogGrep,
		ParallelSafe: true,
	}
}

func handleLogGrep(args map[string]any) (string, error) {
	path, ok := args["path"].(string)
	if !ok {
		return "", fmt.Errorf("path is required")
	}
	pattern, ok := args["pattern"].(string)
	if !ok || strings.TrimSpace(pattern) == "" {
		return "", fmt.Errorf("pattern is required")
	}
	if err := validatePath(path); err != nil {
		return "", err
	}
	useRegex, _ := args["regex"].(bool)
	before := boundedIntArg(args, "before", 2, 0, 20)
	after := boundedIntArg(args, "after", 2, 0, 20)
	maxMatches := boundedIntArg(args, "max_matches", 20, 1, 100)
	ignoreCase, _ := args["ignore_case"].(bool)
	maxScanLines := boundedIntArg(args, "max_scan_lines", defaultLogGrepScanLines, 1, maxLogGrepScanLines)
	maxOutputBytes := boundedIntArg(args, "max_output_bytes", defaultLogGrepOutputByte, 1024, maxLogGrepOutputBytes)
	includeMeta, _ := args["include_meta"].(bool)
	displayPattern := pattern

	var re *regexp.Regexp
	var err error
	if useRegex {
		if ignoreCase {
			pattern = "(?i)" + pattern
		}
		re, err = regexp.Compile(pattern)
		if err != nil {
			return "", fmt.Errorf("compile regex: %w", err)
		}
	}
	result, err := streamLogGrep(path, pattern, re, useRegex, ignoreCase, before, after, maxMatches, maxScanLines, maxOutputBytes)
	if err != nil {
		return "", err
	}
	if includeMeta {
		return prettyStructuredValue(map[string]any{
			"output": result.output,
			"meta": map[string]any{
				"matched":             result.matches > 0,
				"matches":             result.matches,
				"scanned_lines":       result.scannedLines,
				"max_matches_reached": result.maxMatchesReached,
				"max_scan_reached":    result.maxScanReached,
				"output_truncated":    result.outputTruncated,
			},
		})
	}
	if result.matches == 0 {
		return fmt.Sprintf("No matches for %q in %s", displayPattern, path), nil
	}
	return result.output, nil
}

func readLogTail(path string, lines, maxBytes int) ([]string, map[string]any, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, fmt.Errorf("open log file: %w", err)
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return nil, nil, fmt.Errorf("stat log file: %w", err)
	}
	if info.IsDir() {
		return nil, nil, fmt.Errorf("log path is a directory: %s", path)
	}

	size := info.Size()
	meta := map[string]any{
		"path":           path,
		"file_size":      size,
		"max_bytes":      maxBytes,
		"returned_lines": 0,
		"truncated":      false,
	}
	if size == 0 {
		return []string{}, meta, nil
	}

	var data []byte
	pos := size
	newlines := 0
	for pos > 0 && newlines < lines && len(data) < maxBytes {
		readSize := minInt(logTailReadBlockBytes, maxBytes-len(data))
		if int64(readSize) > pos {
			readSize = int(pos)
		}
		pos -= int64(readSize)
		chunk := make([]byte, readSize)
		if _, err := f.ReadAt(chunk, pos); err != nil && err != io.EOF {
			return nil, nil, fmt.Errorf("read log tail: %w", err)
		}
		newlines += bytes.Count(chunk, []byte{'\n'})
		data = append(chunk, data...)
	}
	if bytes.IndexByte(data, 0) >= 0 {
		return nil, nil, fmt.Errorf("log file appears to be binary")
	}

	truncated := pos > 0 && len(data) >= maxBytes
	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	parts := strings.Split(text, "\n")
	if len(parts) > 0 && parts[len(parts)-1] == "" {
		parts = parts[:len(parts)-1]
	}
	if pos > 0 && len(parts) > lines {
		parts = parts[1:]
	}
	if len(parts) > lines {
		parts = parts[len(parts)-lines:]
	}
	meta["returned_lines"] = len(parts)
	meta["truncated"] = truncated
	return parts, meta, nil
}

func countTextFileLines(path string) (int, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, fmt.Errorf("open log file for line count: %w", err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), maxLogScannerTokenBytes)
	lines := 0
	for scanner.Scan() {
		lines++
	}
	if err := scanner.Err(); err != nil {
		return 0, fmt.Errorf("count log lines: %w", err)
	}
	return lines, nil
}

type logLine struct {
	no   int
	text string
}

type logGrepResult struct {
	output            string
	matches           int
	scannedLines      int
	maxMatchesReached bool
	maxScanReached    bool
	outputTruncated   bool
}

func streamLogGrep(path, pattern string, re *regexp.Regexp, useRegex, ignoreCase bool, before, after, maxMatches, maxScanLines, maxOutputBytes int) (logGrepResult, error) {
	f, err := os.Open(path)
	if err != nil {
		return logGrepResult{}, fmt.Errorf("open log file: %w", err)
	}
	defer f.Close()

	var result logGrepResult
	var b strings.Builder
	beforeBuf := make([]logLine, 0, before)
	lastIncluded := 0
	afterUntil := 0
	if !useRegex && ignoreCase {
		pattern = strings.ToLower(pattern)
	}

	writeRaw := func(s string) bool {
		if b.Len()+len(s) > maxOutputBytes {
			result.outputTruncated = true
			return false
		}
		b.WriteString(s)
		return true
	}
	writeLine := func(line logLine, prefix string) bool {
		if line.no <= lastIncluded {
			return true
		}
		if b.Len() > 0 && line.no > lastIncluded+1 {
			if !writeRaw("\n---\n") {
				return false
			}
		}
		if !writeRaw(fmt.Sprintf("%s%d| %s\n", prefix, line.no, line.text)) {
			return false
		}
		lastIncluded = line.no
		return true
	}

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), maxLogScannerTokenBytes)
	for scanner.Scan() {
		result.scannedLines++
		if result.scannedLines > maxScanLines {
			result.maxScanReached = true
			break
		}

		line := strings.TrimSuffix(scanner.Text(), "\r")
		current := logLine{no: result.scannedLines, text: line}
		matched := false
		if result.matches < maxMatches {
			if useRegex {
				matched = re.MatchString(line)
			} else if ignoreCase {
				matched = strings.Contains(strings.ToLower(line), pattern)
			} else {
				matched = strings.Contains(line, pattern)
			}
		}

		if matched {
			result.matches++
			start := current.no - before
			for _, prior := range beforeBuf {
				if prior.no >= start && !writeLine(prior, "  ") {
					result.output = strings.TrimRight(b.String(), "\n")
					return result, nil
				}
			}
			if !writeLine(current, "> ") {
				result.output = strings.TrimRight(b.String(), "\n")
				return result, nil
			}
			afterUntil = maxInt(afterUntil, current.no+after)
			if result.matches == maxMatches {
				result.maxMatchesReached = true
			}
		} else if current.no <= afterUntil {
			if !writeLine(current, "  ") {
				result.output = strings.TrimRight(b.String(), "\n")
				return result, nil
			}
		}

		if before > 0 {
			beforeBuf = append(beforeBuf, current)
			if len(beforeBuf) > before {
				beforeBuf = beforeBuf[len(beforeBuf)-before:]
			}
		}
		if result.matches >= maxMatches && current.no >= afterUntil {
			break
		}
	}
	if err := scanner.Err(); err != nil {
		return logGrepResult{}, fmt.Errorf("scan log file: %w", err)
	}
	result.output = strings.TrimRight(b.String(), "\n")
	return result, nil
}

// HTTPRequestTool performs a controlled HTTP request and returns the response body.
func HTTPRequestTool() *Tool {
	return &Tool{
		Name:        "http_request",
		Description: "Send an HTTP request to a public URL and return the response. Use this for JSON APIs or endpoints that are not best handled by web_fetch.",
		Category:    CatBuiltin,
		Source:      "builtin",
		Permission:  PermApprove,
		Parameters: map[string]Param{
			"url":                {Type: "string", Description: "HTTP or HTTPS URL to request.", Required: true},
			"method":             {Type: "string", Description: "HTTP method. GET, HEAD, and OPTIONS are allowed by default; mutation methods require allow_mutation=true.", Required: false, Default: "GET"},
			"headers_json":       {Type: "string", Description: "Optional JSON object of request headers.", Required: false},
			"body":               {Type: "string", Description: "Optional request body, max 1 MiB.", Required: false},
			"timeout":            {Type: "number", Description: "Timeout in seconds (default 15, max 60).", Required: false, Default: 15},
			"allow_mutation":     {Type: "boolean", Description: "Allow POST, PUT, PATCH, or DELETE.", Required: false, Default: false},
			"max_response_bytes": {Type: "number", Description: "Maximum response bytes to read (default 32768, max 1048576).", Required: false, Default: defaultHTTPResponseBytes},
			"format":             {Type: "string", Description: "Output format: text or json. Default text.", Required: false, Default: "text"},
			"include_headers":    {Type: "boolean", Description: "Include response headers in output.", Required: false, Default: false},
			"redact_headers":     {Type: "boolean", Description: "Redact sensitive response headers when include_headers=true.", Required: false, Default: true},
		},
		Handler:      handleHTTPRequest,
		ParallelSafe: true,
	}
}

func handleHTTPRequest(args map[string]any) (string, error) {
	rawURL, ok := args["url"].(string)
	if !ok || strings.TrimSpace(rawURL) == "" {
		return "", fmt.Errorf("url is required")
	}
	if err := validateFetchURL(rawURL); err != nil {
		return "", err
	}
	method := "GET"
	if m, ok := args["method"].(string); ok && strings.TrimSpace(m) != "" {
		method = strings.ToUpper(strings.TrimSpace(m))
	}
	allowMutation, _ := args["allow_mutation"].(bool)
	if err := validateHTTPMethod(method, allowMutation); err != nil {
		return "", err
	}
	timeout := boundedIntArg(args, "timeout", 15, 1, 60)
	maxResponseBytes := boundedIntArg(args, "max_response_bytes", defaultHTTPResponseBytes, 1, maxHTTPResponseBytes)
	format := strings.ToLower(strings.TrimSpace(stringValue(args["format"])))
	if format == "" {
		format = "text"
	}
	if format != "text" && format != "json" {
		return "", fmt.Errorf("format must be text or json")
	}
	includeHeaders, _ := args["include_headers"].(bool)
	redactHeaders := true
	if raw, ok := args["redact_headers"].(bool); ok {
		redactHeaders = raw
	}
	body, _ := args["body"].(string)
	if len(body) > maxHTTPRequestBodyBytes {
		return "", fmt.Errorf("request body is %d bytes, above max %d", len(body), maxHTTPRequestBodyBytes)
	}

	req, err := http.NewRequestWithContext(context.Background(), method, rawURL, strings.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("User-Agent", "luckyagent-http-request")

	if rawHeaders, ok := args["headers_json"].(string); ok && strings.TrimSpace(rawHeaders) != "" {
		var headers map[string]string
		if err := json.Unmarshal([]byte(rawHeaders), &headers); err != nil {
			return "", fmt.Errorf("parse headers_json: %w", err)
		}
		for k, v := range headers {
			if err := validateRequestHeader(k, v); err != nil {
				return "", err
			}
			req.Header.Set(k, v)
		}
	}

	client := &http.Client{
		Timeout: time.Duration(timeout) * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return fmt.Errorf("stopped after 10 redirects")
			}
			if err := validateFetchURL(req.URL.String()); err != nil {
				return fmt.Errorf("redirect url validation failed: %w", err)
			}
			return nil
		},
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("send request: %w", err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(io.LimitReader(resp.Body, int64(maxResponseBytes)+1))
	if err != nil {
		return "", fmt.Errorf("read response: %w", err)
	}
	truncated := len(data) > maxResponseBytes
	if truncated {
		data = data[:maxResponseBytes]
	}
	trimmedData := bytes.TrimSpace(data)
	bodyText := string(trimmedData)
	var jsonBody any
	bodyIsJSON := len(trimmedData) > 0 && json.Valid(trimmedData)
	if bodyIsJSON {
		_ = json.Unmarshal(trimmedData, &jsonBody)
		var pretty bytes.Buffer
		if err := json.Indent(&pretty, trimmedData, "", "  "); err == nil {
			bodyText = pretty.String()
		}
	}
	if format == "json" {
		result := map[string]any{
			"status":         resp.Status,
			"status_code":    resp.StatusCode,
			"content_type":   resp.Header.Get("Content-Type"),
			"bytes_read":     len(data),
			"response_limit": maxResponseBytes,
			"truncated":      truncated,
		}
		if bodyIsJSON {
			result["body"] = jsonBody
		} else {
			result["body"] = bodyText
		}
		if includeHeaders {
			result["headers"] = responseHeaders(resp.Header, redactHeaders)
		}
		return prettyStructuredValue(result)
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf("Status: %s\n", resp.Status))
	if ct := strings.TrimSpace(resp.Header.Get("Content-Type")); ct != "" {
		b.WriteString("Content-Type: " + ct + "\n")
	}
	b.WriteString(fmt.Sprintf("Bytes-Read: %d\n", len(data)))
	if truncated {
		b.WriteString(fmt.Sprintf("Truncated: true (limit %d bytes)\n", maxResponseBytes))
	}
	if includeHeaders {
		b.WriteString("Headers:\n")
		headers := responseHeaders(resp.Header, redactHeaders)
		names := make([]string, 0, len(headers))
		for name := range headers {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			b.WriteString(fmt.Sprintf("  %s: %s\n", name, strings.Join(headers[name], ", ")))
		}
	}
	if bodyText != "" {
		b.WriteString("\n")
		b.WriteString(utils.Truncate(bodyText, 12000))
	}
	return strings.TrimSpace(b.String()), nil
}

func validateHTTPMethod(method string, allowMutation bool) error {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return nil
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		if allowMutation {
			return nil
		}
		return fmt.Errorf("%s requires allow_mutation=true", method)
	default:
		return fmt.Errorf("unsupported HTTP method %q", method)
	}
}

func validateRequestHeader(name, value string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("header name is required")
	}
	for _, r := range name {
		if r <= 31 || r == 127 || r == ':' {
			return fmt.Errorf("header %q contains invalid characters", name)
		}
	}
	for _, r := range value {
		if r == '\r' || r == '\n' {
			return fmt.Errorf("header %q contains newline characters", name)
		}
	}
	switch strings.ToLower(name) {
	case "host", "connection", "content-length", "transfer-encoding":
		return fmt.Errorf("header %q cannot be overridden", name)
	default:
		return nil
	}
}

func responseHeaders(headers http.Header, redact bool) map[string][]string {
	out := make(map[string][]string, len(headers))
	for name, values := range headers {
		copied := append([]string(nil), values...)
		if redact && isSensitiveHeader(name) {
			for i := range copied {
				copied[i] = "[REDACTED]"
			}
		}
		out[name] = copied
	}
	return out
}

func isSensitiveHeader(name string) bool {
	switch strings.ToLower(name) {
	case "authorization", "proxy-authorization", "cookie", "set-cookie", "x-api-key", "x-auth-token":
		return true
	default:
		return false
	}
}

// JSONQueryTool extracts a nested field from a JSON document using dot-path syntax.
func JSONQueryTool() *Tool {
	return &Tool{
		Name:        "json_query",
		Description: "Read a JSON file and extract a nested value using dot-path syntax like items[0].name.",
		Category:    CatBuiltin,
		Source:      "builtin",
		Permission:  PermAuto,
		Parameters: map[string]Param{
			"path":             {Type: "string", Description: "Path to the JSON file.", Required: true},
			"query":            {Type: "string", Description: "Dot-path query such as user.name, items[0].id, metadata[\"app.name\"], or items[*].id. Leave empty to pretty-print the full document.", Required: false},
			"max_file_bytes":   {Type: "number", Description: "Maximum JSON file size to read (default 20 MiB, max 100 MiB).", Required: false, Default: defaultStructuredMaxFile},
			"max_output_chars": {Type: "number", Description: "Maximum output characters before returning truncation metadata (default 12000, max 1048576).", Required: false, Default: defaultStructuredOutput},
			"summary":          {Type: "boolean", Description: "Return a top-level summary when query is empty.", Required: false, Default: false},
		},
		Handler:      handleJSONQuery,
		ParallelSafe: true,
	}
}

func handleJSONQuery(args map[string]any) (string, error) {
	path, ok := args["path"].(string)
	if !ok {
		return "", fmt.Errorf("path is required")
	}
	if err := validatePath(path); err != nil {
		return "", err
	}
	maxFileBytes := boundedIntArg(args, "max_file_bytes", defaultStructuredMaxFile, 1, maxStructuredFileBytes)
	maxOutputChars := boundedIntArg(args, "max_output_chars", defaultStructuredOutput, 100, maxStructuredOutput)
	data, size, err := readBoundedFile(path, maxFileBytes, "json")
	if err != nil {
		return "", err
	}
	var doc any
	if err := json.Unmarshal(data, &doc); err != nil {
		return "", fmt.Errorf("parse json: %w", err)
	}
	query, _ := args["query"].(string)
	summary, _ := args["summary"].(bool)
	return queryStructuredValue(doc, query, structuredQueryOptions{
		MaxOutputChars: maxOutputChars,
		Summary:        summary,
		SizeBytes:      size,
	})
}

// YAMLQueryTool extracts a nested field from a YAML document using dot-path syntax.
func YAMLQueryTool() *Tool {
	return &Tool{
		Name:        "yaml_query",
		Description: "Read a YAML file and extract a nested value using dot-path syntax like spec.template.metadata.name.",
		Category:    CatBuiltin,
		Source:      "builtin",
		Permission:  PermAuto,
		Parameters: map[string]Param{
			"path":             {Type: "string", Description: "Path to the YAML file.", Required: true},
			"query":            {Type: "string", Description: "Dot-path query such as metadata.name, items[0].id, metadata.labels[\"app.kubernetes.io/name\"], or items[*].metadata.name. Leave empty to pretty-print the selected document.", Required: false},
			"document":         {Type: "number", Description: "Zero-based YAML document index to query (default 0).", Required: false, Default: 0},
			"all_documents":    {Type: "boolean", Description: "Apply the query to all YAML documents and return an array.", Required: false, Default: false},
			"max_file_bytes":   {Type: "number", Description: "Maximum YAML file size to read (default 20 MiB, max 100 MiB).", Required: false, Default: defaultStructuredMaxFile},
			"max_output_chars": {Type: "number", Description: "Maximum output characters before returning truncation metadata (default 12000, max 1048576).", Required: false, Default: defaultStructuredOutput},
			"summary":          {Type: "boolean", Description: "Return a document summary when query is empty.", Required: false, Default: false},
		},
		Handler:      handleYAMLQuery,
		ParallelSafe: true,
	}
}

func handleYAMLQuery(args map[string]any) (string, error) {
	path, ok := args["path"].(string)
	if !ok {
		return "", fmt.Errorf("path is required")
	}
	if err := validatePath(path); err != nil {
		return "", err
	}
	maxFileBytes := boundedIntArg(args, "max_file_bytes", defaultStructuredMaxFile, 1, maxStructuredFileBytes)
	maxOutputChars := boundedIntArg(args, "max_output_chars", defaultStructuredOutput, 100, maxStructuredOutput)
	data, size, err := readBoundedFile(path, maxFileBytes, "yaml")
	if err != nil {
		return "", err
	}
	docs, err := decodeYAMLDocuments(data)
	if err != nil {
		return "", err
	}
	if len(docs) == 0 {
		return "", fmt.Errorf("yaml file has no documents")
	}

	query, _ := args["query"].(string)
	allDocuments, _ := args["all_documents"].(bool)
	summary, _ := args["summary"].(bool)
	if allDocuments {
		if strings.TrimSpace(query) == "" && summary {
			return formatStructuredValue(yamlDocumentsSummary(docs, size), maxOutputChars)
		}
		values := make([]any, 0, len(docs))
		for i, doc := range docs {
			value, err := queryStructuredRaw(doc, query, structuredQueryOptions{})
			if err != nil {
				return "", fmt.Errorf("document %d: %w", i, err)
			}
			values = append(values, value)
		}
		return formatStructuredValue(values, maxOutputChars)
	}

	document, _ := intArg(args, "document", 0)
	if document < 0 || document >= len(docs) {
		return "", fmt.Errorf("document index %d out of range; yaml file has %d documents", document, len(docs))
	}
	return queryStructuredValue(docs[document], query, structuredQueryOptions{
		MaxOutputChars: maxOutputChars,
		Summary:        summary,
		SizeBytes:      size,
		YAMLDocuments:  docs,
	})
}

// CSVQueryTool returns rows from a CSV file, optionally filtered by one column.
func CSVQueryTool() *Tool {
	return &Tool{
		Name:        "csv_query",
		Description: "Stream a CSV file and optionally filter rows, project columns, and return bounded JSON results.",
		Category:    CatBuiltin,
		Source:      "builtin",
		Permission:  PermAuto,
		Parameters: map[string]Param{
			"path":          {Type: "string", Description: "Path to the CSV file.", Required: true},
			"column":        {Type: "string", Description: "Optional legacy column name for equals/contains/regex filters.", Required: false},
			"equals":        {Type: "string", Description: "Optional exact string to match with column.", Required: false},
			"contains":      {Type: "string", Description: "Optional substring to match with column.", Required: false},
			"regex":         {Type: "string", Description: "Optional regular expression to match with column.", Required: false},
			"columns":       {Type: "array", Description: "Optional list of columns to include in output.", Required: false},
			"filters":       {Type: "array", Description: "Optional filters with column, op, and value. Supported ops: eq, neq, contains, prefix, suffix, regex, empty, not_empty, gt, gte, lt, lte.", Required: false},
			"limit":         {Type: "number", Description: "Maximum number of rows to return (default 20, max 100).", Required: false, Default: 20},
			"max_scan_rows": {Type: "number", Description: "Maximum data rows to scan (default 100000, max 1000000).", Required: false, Default: defaultCSVMaxScanRows},
			"delimiter":     {Type: "string", Description: "Single-rune delimiter, or \\t for TSV. Default comma.", Required: false, Default: ","},
			"comment":       {Type: "string", Description: "Optional single-rune comment marker.", Required: false},
			"trim_space":    {Type: "boolean", Description: "Trim leading parser spaces and surrounding field spaces.", Required: false, Default: false},
			"lazy_quotes":   {Type: "boolean", Description: "Allow lazy quote parsing for non-standard CSV.", Required: false, Default: false},
			"include_meta":  {Type: "boolean", Description: "Return rows with scanned/matched/truncated metadata.", Required: false, Default: false},
		},
		Handler:      handleCSVQuery,
		ParallelSafe: true,
	}
}

func handleCSVQuery(args map[string]any) (string, error) {
	path, ok := args["path"].(string)
	if !ok {
		return "", fmt.Errorf("path is required")
	}
	if err := validatePath(path); err != nil {
		return "", err
	}
	limit := boundedIntArg(args, "limit", 20, 1, 100)
	maxScanRows := boundedIntArg(args, "max_scan_rows", defaultCSVMaxScanRows, 1, maxCSVMaxScanRows)
	trimSpace, _ := args["trim_space"].(bool)
	lazyQuotes, _ := args["lazy_quotes"].(bool)
	includeMeta, _ := args["include_meta"].(bool)
	delimiter, err := csvRuneArg(args, "delimiter", ',')
	if err != nil {
		return "", err
	}
	comment, err := csvOptionalRuneArg(args, "comment")
	if err != nil {
		return "", err
	}

	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("open csv file: %w", err)
	}
	defer f.Close()

	reader := csv.NewReader(f)
	reader.FieldsPerRecord = -1
	reader.Comma = delimiter
	reader.Comment = comment
	reader.TrimLeadingSpace = trimSpace
	reader.LazyQuotes = lazyQuotes

	headers, err := reader.Read()
	if err != nil {
		if err == io.EOF {
			return "", fmt.Errorf("csv is empty")
		}
		return "", fmt.Errorf("read csv: %w", err)
	}
	if trimSpace {
		trimCSVRecord(headers)
	}
	if len(headers) == 0 {
		return "", fmt.Errorf("csv is empty")
	}
	headerIndex := make(map[string]int, len(headers))
	for i, h := range headers {
		headerIndex[h] = i
	}
	projection, err := csvProjection(args, headers, headerIndex)
	if err != nil {
		return "", err
	}
	filters, err := csvFilters(args, headerIndex)
	if err != nil {
		return "", err
	}

	var out []map[string]string
	scannedRows := 0
	matchedRows := 0
	scanLimited := false
	for scannedRows < maxScanRows {
		row, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", fmt.Errorf("read csv row %d: %w", scannedRows+2, err)
		}
		scannedRows++
		if trimSpace {
			trimCSVRecord(row)
		}
		if !csvRowMatches(row, filters) {
			continue
		}
		matchedRows++
		if len(out) < limit {
			entry := make(map[string]string, len(projection))
			for _, col := range projection {
				i := headerIndex[col]
				if i < len(row) {
					entry[col] = row[i]
				} else {
					entry[col] = ""
				}
			}
			out = append(out, entry)
		}
		if len(out) >= limit && !includeMeta {
			break
		}
	}
	if scannedRows >= maxScanRows {
		if _, err := reader.Read(); err != io.EOF {
			if err == nil {
				scanLimited = true
			} else {
				return "", fmt.Errorf("read csv row %d: %w", scannedRows+2, err)
			}
		}
	}
	truncated := matchedRows > len(out) || scanLimited
	if includeMeta {
		return prettyStructuredValue(map[string]any{
			"rows": out,
			"meta": map[string]any{
				"scanned_rows":  scannedRows,
				"matched_rows":  matchedRows,
				"returned_rows": len(out),
				"max_scan_rows": maxScanRows,
				"scan_limited":  scanLimited,
				"truncated":     truncated,
			},
		})
	}
	return prettyStructuredValue(out)
}

func csvProjection(args map[string]any, headers []string, headerIndex map[string]int) ([]string, error) {
	columns, err := stringListArg(args["columns"])
	if err != nil {
		return nil, fmt.Errorf("columns: %w", err)
	}
	if len(columns) == 0 {
		return headers, nil
	}
	out := make([]string, 0, len(columns))
	for _, col := range columns {
		if _, ok := headerIndex[col]; !ok {
			return nil, fmt.Errorf("column %q not found", col)
		}
		out = append(out, col)
	}
	return out, nil
}

type csvFilter struct {
	column string
	index  int
	op     string
	value  string
	re     *regexp.Regexp
}

func csvFilters(args map[string]any, headerIndex map[string]int) ([]csvFilter, error) {
	var filters []csvFilter
	specs, err := csvFilterSpecs(args["filters"])
	if err != nil {
		return nil, err
	}
	for _, spec := range specs {
		filter, err := newCSVFilter(spec, headerIndex)
		if err != nil {
			return nil, err
		}
		filters = append(filters, filter)
	}

	column, _ := args["column"].(string)
	column = strings.TrimSpace(column)
	if column == "" {
		return filters, nil
	}
	if _, ok := headerIndex[column]; !ok {
		return nil, fmt.Errorf("column %q not found", column)
	}
	if equals, _ := args["equals"].(string); strings.TrimSpace(equals) != "" {
		filter, err := newCSVFilter(map[string]any{"column": column, "op": "eq", "value": equals}, headerIndex)
		if err != nil {
			return nil, err
		}
		filters = append(filters, filter)
	}
	if contains, _ := args["contains"].(string); strings.TrimSpace(contains) != "" {
		filter, err := newCSVFilter(map[string]any{"column": column, "op": "contains", "value": contains}, headerIndex)
		if err != nil {
			return nil, err
		}
		filters = append(filters, filter)
	}
	if regexText, _ := args["regex"].(string); strings.TrimSpace(regexText) != "" {
		filter, err := newCSVFilter(map[string]any{"column": column, "op": "regex", "value": regexText}, headerIndex)
		if err != nil {
			return nil, err
		}
		filters = append(filters, filter)
	}
	return filters, nil
}

func csvFilterSpecs(raw any) ([]map[string]any, error) {
	if raw == nil {
		return nil, nil
	}
	switch v := raw.(type) {
	case string:
		if strings.TrimSpace(v) == "" {
			return nil, nil
		}
		var specs []map[string]any
		if err := json.Unmarshal([]byte(v), &specs); err != nil {
			return nil, fmt.Errorf("parse filters: %w", err)
		}
		return specs, nil
	case []map[string]any:
		return v, nil
	case []any:
		specs := make([]map[string]any, 0, len(v))
		for i, item := range v {
			spec, ok := item.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("filter %d must be an object", i)
			}
			specs = append(specs, spec)
		}
		return specs, nil
	default:
		return nil, fmt.Errorf("filters must be an array or JSON array string")
	}
}

func newCSVFilter(spec map[string]any, headerIndex map[string]int) (csvFilter, error) {
	column := strings.TrimSpace(stringValue(spec["column"]))
	if column == "" {
		return csvFilter{}, fmt.Errorf("filter column is required")
	}
	index, ok := headerIndex[column]
	if !ok {
		return csvFilter{}, fmt.Errorf("column %q not found", column)
	}
	op := strings.ToLower(strings.TrimSpace(stringValue(spec["op"])))
	if op == "" {
		op = "eq"
	}
	value := stringValue(spec["value"])
	filter := csvFilter{column: column, index: index, op: op, value: value}
	switch op {
	case "eq", "neq", "contains", "prefix", "suffix", "empty", "not_empty", "gt", "gte", "lt", "lte":
		return filter, nil
	case "regex":
		re, err := regexp.Compile(value)
		if err != nil {
			return csvFilter{}, fmt.Errorf("compile regex for column %q: %w", column, err)
		}
		filter.re = re
		return filter, nil
	default:
		return csvFilter{}, fmt.Errorf("unsupported filter op %q", op)
	}
}

func csvRowMatches(row []string, filters []csvFilter) bool {
	for _, filter := range filters {
		value := ""
		if filter.index < len(row) {
			value = row[filter.index]
		}
		if !csvFilterMatches(value, filter) {
			return false
		}
	}
	return true
}

func csvFilterMatches(value string, filter csvFilter) bool {
	switch filter.op {
	case "eq":
		return value == filter.value
	case "neq":
		return value != filter.value
	case "contains":
		return strings.Contains(value, filter.value)
	case "prefix":
		return strings.HasPrefix(value, filter.value)
	case "suffix":
		return strings.HasSuffix(value, filter.value)
	case "regex":
		return filter.re.MatchString(value)
	case "empty":
		return value == ""
	case "not_empty":
		return value != ""
	case "gt", "gte", "lt", "lte":
		left, err := strconv.ParseFloat(value, 64)
		if err != nil {
			return false
		}
		right, err := strconv.ParseFloat(filter.value, 64)
		if err != nil {
			return false
		}
		switch filter.op {
		case "gt":
			return left > right
		case "gte":
			return left >= right
		case "lt":
			return left < right
		case "lte":
			return left <= right
		}
	}
	return false
}

func stringValue(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case fmt.Stringer:
		return x.String()
	case nil:
		return ""
	default:
		return fmt.Sprint(x)
	}
}

func stringListArg(raw any) ([]string, error) {
	if raw == nil {
		return nil, nil
	}
	switch v := raw.(type) {
	case []string:
		return compactStringList(v), nil
	case []any:
		out := make([]string, 0, len(v))
		for i, item := range v {
			s, ok := item.(string)
			if !ok {
				return nil, fmt.Errorf("item %d must be a string", i)
			}
			out = append(out, s)
		}
		return compactStringList(out), nil
	case string:
		if strings.TrimSpace(v) == "" {
			return nil, nil
		}
		var out []string
		if strings.HasPrefix(strings.TrimSpace(v), "[") {
			if err := json.Unmarshal([]byte(v), &out); err != nil {
				return nil, fmt.Errorf("parse JSON list: %w", err)
			}
			return compactStringList(out), nil
		}
		return compactStringList(strings.Split(v, ",")), nil
	default:
		return nil, fmt.Errorf("must be an array, JSON array string, or comma-separated string")
	}
}

func compactStringList(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			out = append(out, value)
		}
	}
	return out
}

func csvRuneArg(args map[string]any, key string, def rune) (rune, error) {
	raw, _ := args[key].(string)
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return def, nil
	}
	return parseSingleRune(raw, key)
}

func csvOptionalRuneArg(args map[string]any, key string) (rune, error) {
	raw, _ := args[key].(string)
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, nil
	}
	return parseSingleRune(raw, key)
}

func parseSingleRune(raw, name string) (rune, error) {
	if raw == `\t` {
		return '\t', nil
	}
	runes := []rune(raw)
	if len(runes) != 1 {
		return 0, fmt.Errorf("%s must be a single rune", name)
	}
	return runes[0], nil
}

func trimCSVRecord(record []string) {
	for i := range record {
		record[i] = strings.TrimSpace(record[i])
	}
}

// SQLQueryTool executes a read-only SQL query against a local SQLite database.
func SQLQueryTool() *Tool {
	return &Tool{
		Name:        "sql_query",
		Description: "Execute a read-only SQL query against a local SQLite database file.",
		Category:    CatBuiltin,
		Source:      "builtin",
		Permission:  PermApprove,
		Parameters: map[string]Param{
			"path":            {Type: "string", Description: "Path to the SQLite database file.", Required: true},
			"query":           {Type: "string", Description: "Read-only SQL query (SELECT, WITH, allowlisted PRAGMA, EXPLAIN read query).", Required: true},
			"limit":           {Type: "number", Description: "Maximum number of rows to return (default 50, max 200).", Required: false, Default: 50},
			"timeout_seconds": {Type: "number", Description: "Query timeout in seconds (default 10, max 60).", Required: false, Default: defaultSQLTimeoutSeconds},
			"include_meta":    {Type: "boolean", Description: "Return rows with columns, row count, truncation, and duration metadata.", Required: false, Default: false},
		},
		Handler:      handleSQLQuery,
		ParallelSafe: true,
	}
}

func handleSQLQuery(args map[string]any) (string, error) {
	path, ok := args["path"].(string)
	if !ok {
		return "", fmt.Errorf("path is required")
	}
	query, ok := args["query"].(string)
	if !ok || strings.TrimSpace(query) == "" {
		return "", fmt.Errorf("query is required")
	}
	if err := validatePath(path); err != nil {
		return "", err
	}
	if err := validateReadOnlySQL(query); err != nil {
		return "", err
	}
	limit := boundedIntArg(args, "limit", 50, 1, 200)
	timeoutSeconds := boundedIntArg(args, "timeout_seconds", defaultSQLTimeoutSeconds, 1, maxSQLTimeoutSeconds)
	includeMeta, _ := args["include_meta"].(bool)

	db, err := sql.Open("sqlite3", sqliteReadOnlyDSN(path))
	if err != nil {
		return "", fmt.Errorf("open sqlite database: %w", err)
	}
	defer db.Close()
	if _, err := db.Exec("PRAGMA query_only = ON"); err != nil {
		return "", fmt.Errorf("enable sqlite query_only: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutSeconds)*time.Second)
	defer cancel()
	start := time.Now()
	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return "", fmt.Errorf("query sqlite database: %w", err)
	}
	defer rows.Close()

	result, columns, truncated, err := scanSQLRows(rows, limit)
	if err != nil {
		return "", err
	}
	if includeMeta {
		return prettyStructuredValue(map[string]any{
			"rows": result,
			"meta": map[string]any{
				"columns":       columns,
				"returned_rows": len(result),
				"limit":         limit,
				"truncated":     truncated,
				"duration_ms":   time.Since(start).Milliseconds(),
			},
		})
	}
	return prettyStructuredValue(result)
}

// DBSchemaTool inspects the schema of a local SQLite database.
func DBSchemaTool() *Tool {
	return &Tool{
		Name:        "db_schema",
		Description: "Inspect the schema of a local SQLite database, including tables and columns.",
		Category:    CatBuiltin,
		Source:      "builtin",
		Permission:  PermAuto,
		Parameters: map[string]Param{
			"path":             {Type: "string", Description: "Path to the SQLite database file.", Required: true},
			"table":            {Type: "string", Description: "Optional specific table name.", Required: false},
			"include":          {Type: "string", Description: "Comma-separated sections to include: columns,indexes,foreign_keys,views,triggers. Default columns.", Required: false, Default: "columns"},
			"limit_tables":     {Type: "number", Description: "Maximum tables to return when table is omitted (default 100, max 1000).", Required: false, Default: defaultDBSchemaTableMax},
			"include_internal": {Type: "boolean", Description: "Include sqlite_% internal tables.", Required: false, Default: false},
			"include_sql":      {Type: "boolean", Description: "Include raw SQL for views and triggers.", Required: false, Default: false},
		},
		Handler:      handleDBSchema,
		ParallelSafe: true,
	}
}

func handleDBSchema(args map[string]any) (string, error) {
	path, ok := args["path"].(string)
	if !ok {
		return "", fmt.Errorf("path is required")
	}
	if err := validatePath(path); err != nil {
		return "", err
	}
	table, _ := args["table"].(string)
	include, err := dbSchemaIncludes(args["include"])
	if err != nil {
		return "", err
	}
	limitTables := boundedIntArg(args, "limit_tables", defaultDBSchemaTableMax, 1, maxDBSchemaTableMax)
	includeInternal, _ := args["include_internal"].(bool)
	includeSQL, _ := args["include_sql"].(bool)

	db, err := sql.Open("sqlite3", sqliteReadOnlyDSN(path))
	if err != nil {
		return "", fmt.Errorf("open sqlite database: %w", err)
	}
	defer db.Close()
	if _, err := db.Exec("PRAGMA query_only = ON"); err != nil {
		return "", fmt.Errorf("enable sqlite query_only: %w", err)
	}

	if strings.TrimSpace(table) != "" {
		schema, err := sqliteTableDetails(db, table, include)
		if err != nil {
			return "", err
		}
		if include["triggers"] {
			triggers, err := sqliteObjects(db, "trigger", table, includeInternal, includeSQL, 0)
			if err != nil {
				return "", err
			}
			schema["triggers"] = triggers
		}
		result := map[string]any{
			"table": table,
		}
		for k, v := range schema {
			result[k] = v
		}
		return prettyStructuredValue(result)
	}

	tableQuery := `SELECT name FROM sqlite_master WHERE type='table'`
	if !includeInternal {
		tableQuery += ` AND name NOT LIKE 'sqlite_%'`
	}
	tableQuery += ` ORDER BY name LIMIT ?`
	rows, err := db.Query(tableQuery, limitTables)
	if err != nil {
		return "", fmt.Errorf("list tables: %w", err)
	}
	defer rows.Close()

	var tables []map[string]any
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return "", fmt.Errorf("scan table name: %w", err)
		}
		schema, err := sqliteTableDetails(db, name, include)
		if err != nil {
			return "", err
		}
		entry := map[string]any{"name": name}
		for k, v := range schema {
			entry[k] = v
		}
		tables = append(tables, entry)
	}
	result := map[string]any{
		"tables": tables,
	}
	if include["views"] {
		views, err := sqliteObjects(db, "view", "", includeInternal, includeSQL, limitTables)
		if err != nil {
			return "", err
		}
		result["views"] = views
	}
	if include["triggers"] {
		triggers, err := sqliteObjects(db, "trigger", "", includeInternal, includeSQL, limitTables)
		if err != nil {
			return "", err
		}
		result["triggers"] = triggers
	}
	return prettyStructuredValue(result)
}

type structuredQueryOptions struct {
	MaxOutputChars int
	Summary        bool
	SizeBytes      int64
	YAMLDocuments  []any
}

func readBoundedFile(path string, maxBytes int, kind string) ([]byte, int64, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, 0, fmt.Errorf("stat %s file: %w", kind, err)
	}
	if info.IsDir() {
		return nil, 0, fmt.Errorf("%s path is a directory: %s", kind, path)
	}
	if info.Size() > int64(maxBytes) {
		return nil, info.Size(), fmt.Errorf("%s file is %d bytes, above max_file_bytes %d", kind, info.Size(), maxBytes)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, info.Size(), fmt.Errorf("read %s file: %w", kind, err)
	}
	return data, info.Size(), nil
}

func decodeYAMLDocuments(data []byte) ([]any, error) {
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	var docs []any
	for {
		var doc any
		err := decoder.Decode(&doc)
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("parse yaml: %w", err)
		}
		docs = append(docs, normalizeYAMLValue(doc))
	}
	return docs, nil
}

func queryStructuredValue(doc any, query any, opts structuredQueryOptions) (string, error) {
	value, err := queryStructuredRaw(doc, query, opts)
	if err != nil {
		return "", err
	}
	return formatStructuredValue(value, opts.MaxOutputChars)
}

func queryStructuredRaw(doc any, query any, opts structuredQueryOptions) (any, error) {
	queryText, _ := query.(string)
	queryText = strings.TrimSpace(queryText)
	if len(queryText) > maxStructuredQueryChars {
		return nil, fmt.Errorf("query is too long: %d characters exceeds %d", len(queryText), maxStructuredQueryChars)
	}
	if queryText == "" {
		if opts.Summary {
			if len(opts.YAMLDocuments) > 0 {
				return yamlDocumentsSummary(opts.YAMLDocuments, opts.SizeBytes), nil
			}
			return structuredValueSummary(doc, opts.SizeBytes), nil
		}
		return doc, nil
	}
	value, err := walkStructuredPath(doc, queryText)
	if err != nil {
		return nil, err
	}
	return value, nil
}

func formatStructuredValue(v any, maxOutputChars int) (string, error) {
	if maxOutputChars <= 0 {
		maxOutputChars = defaultStructuredOutput
	}
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal result: %w", err)
	}
	if len(data) <= maxOutputChars {
		return string(data), nil
	}
	truncated := truncateUTF8String(string(data), maxOutputChars)
	return prettyStructuredValue(map[string]any{
		"value": truncated,
		"_meta": map[string]any{
			"truncated":        true,
			"max_output_chars": maxOutputChars,
			"original_chars":   len(string(data)),
		},
	})
}

func truncateUTF8String(s string, maxChars int) string {
	if maxChars <= 0 || len(s) <= maxChars {
		return s
	}
	cut := maxChars
	for cut > 0 && (s[cut]&0xc0) == 0x80 {
		cut--
	}
	if cut <= 0 {
		return ""
	}
	return s[:cut]
}

func structuredValueSummary(v any, sizeBytes int64) map[string]any {
	summary := map[string]any{
		"type":       structuredValueType(v),
		"size_bytes": sizeBytes,
	}
	switch x := v.(type) {
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		summary["keys"] = keys
		summary["key_count"] = len(keys)
	case []any:
		summary["length"] = len(x)
	case string:
		summary["length"] = len(x)
	}
	return summary
}

func yamlDocumentsSummary(docs []any, sizeBytes int64) map[string]any {
	items := make([]map[string]any, 0, len(docs))
	for i, doc := range docs {
		item := map[string]any{
			"index": i,
			"type":  structuredValueType(doc),
		}
		if obj, ok := doc.(map[string]any); ok {
			if kind, ok := obj["kind"].(string); ok {
				item["kind"] = kind
			}
			if metadata, ok := obj["metadata"].(map[string]any); ok {
				if name, ok := metadata["name"].(string); ok {
					item["name"] = name
				}
			}
		}
		items = append(items, item)
	}
	return map[string]any{
		"type":       "yaml_documents",
		"size_bytes": sizeBytes,
		"documents":  items,
	}
}

func structuredValueType(v any) string {
	switch v.(type) {
	case map[string]any:
		return "object"
	case []any:
		return "array"
	case string:
		return "string"
	case float64, float32, int, int64, int32, uint, uint64, uint32:
		return "number"
	case bool:
		return "boolean"
	case nil:
		return "null"
	default:
		return fmt.Sprintf("%T", v)
	}
}

func normalizeYAMLValue(v any) any {
	switch x := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, val := range x {
			out[k] = normalizeYAMLValue(val)
		}
		return out
	case map[any]any:
		out := make(map[string]any, len(x))
		for k, val := range x {
			out[fmt.Sprint(k)] = normalizeYAMLValue(val)
		}
		return out
	case []any:
		out := make([]any, 0, len(x))
		for _, item := range x {
			out = append(out, normalizeYAMLValue(item))
		}
		return out
	default:
		return v
	}
}

func walkStructuredPath(doc any, query string) (any, error) {
	ops, err := parseStructuredPath(query)
	if err != nil {
		return nil, err
	}
	return walkStructuredOps(doc, ops, "")
}

type structuredPathOpKind int

const (
	structuredPathKey structuredPathOpKind = iota
	structuredPathIndex
	structuredPathWildcard
)

type structuredPathOp struct {
	kind  structuredPathOpKind
	key   string
	index int
}

func parseStructuredPath(query string) ([]structuredPathOp, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, nil
	}
	var ops []structuredPathOp
	for i := 0; i < len(query); {
		switch query[i] {
		case '.':
			i++
			continue
		case '[':
			op, next, err := parseStructuredBracket(query, i)
			if err != nil {
				return nil, err
			}
			ops = append(ops, op)
			i = next
		default:
			start := i
			for i < len(query) && query[i] != '.' && query[i] != '[' {
				i++
			}
			key := strings.TrimSpace(query[start:i])
			if key == "" {
				return nil, fmt.Errorf("empty path token near %q", query[start:])
			}
			ops = append(ops, structuredPathOp{kind: structuredPathKey, key: key})
		}
	}
	return ops, nil
}

func parseStructuredBracket(query string, start int) (structuredPathOp, int, error) {
	i := start + 1
	if i >= len(query) {
		return structuredPathOp{}, 0, fmt.Errorf("unterminated bracket in query")
	}
	if query[i] == '"' || query[i] == '\'' {
		quote := query[i]
		i++
		var b strings.Builder
		for i < len(query) {
			ch := query[i]
			if ch == '\\' && i+1 < len(query) {
				b.WriteByte(query[i+1])
				i += 2
				continue
			}
			if ch == quote {
				i++
				if i >= len(query) || query[i] != ']' {
					return structuredPathOp{}, 0, fmt.Errorf("expected ] after bracket key")
				}
				return structuredPathOp{kind: structuredPathKey, key: b.String()}, i + 1, nil
			}
			b.WriteByte(ch)
			i++
		}
		return structuredPathOp{}, 0, fmt.Errorf("unterminated quoted key in query")
	}
	end := strings.IndexByte(query[i:], ']')
	if end < 0 {
		return structuredPathOp{}, 0, fmt.Errorf("unterminated bracket in query")
	}
	raw := strings.TrimSpace(query[i : i+end])
	next := i + end + 1
	if raw == "*" {
		return structuredPathOp{kind: structuredPathWildcard}, next, nil
	}
	index, err := strconv.Atoi(raw)
	if err != nil {
		return structuredPathOp{}, 0, fmt.Errorf("invalid array index %q", raw)
	}
	return structuredPathOp{kind: structuredPathIndex, index: index}, next, nil
}

func walkStructuredOps(current any, ops []structuredPathOp, at string) (any, error) {
	if len(ops) == 0 {
		return current, nil
	}
	op := ops[0]
	switch op.kind {
	case structuredPathKey:
		if op.key == "length" {
			if length, ok := structuredLength(current); ok {
				return walkStructuredOps(length, ops[1:], appendPathKey(at, op.key))
			}
		}
		obj, ok := current.(map[string]any)
		if !ok {
			return nil, structuredPathError(at, "path %q expected object", op.key)
		}
		next, ok := obj[op.key]
		if !ok {
			return nil, structuredPathError(at, "path key %q not found", op.key)
		}
		return walkStructuredOps(next, ops[1:], appendPathKey(at, op.key))
	case structuredPathIndex:
		arr, ok := current.([]any)
		if !ok {
			return nil, structuredPathError(at, "path index %d expected array", op.index)
		}
		if op.index < 0 || op.index >= len(arr) {
			return nil, structuredPathError(at, "path index %d out of range", op.index)
		}
		return walkStructuredOps(arr[op.index], ops[1:], fmt.Sprintf("%s[%d]", at, op.index))
	case structuredPathWildcard:
		arr, ok := current.([]any)
		if !ok {
			return nil, structuredPathError(at, "path wildcard expected array")
		}
		out := make([]any, 0, len(arr))
		for i, item := range arr {
			value, err := walkStructuredOps(item, ops[1:], fmt.Sprintf("%s[%d]", at, i))
			if err != nil {
				return nil, err
			}
			out = append(out, value)
		}
		return out, nil
	default:
		return nil, fmt.Errorf("unknown structured path operation")
	}
}

func structuredLength(v any) (int, bool) {
	switch x := v.(type) {
	case []any:
		return len(x), true
	case map[string]any:
		return len(x), true
	case string:
		return len(x), true
	default:
		return 0, false
	}
}

func structuredPathError(at, format string, args ...any) error {
	if at == "" {
		at = "<root>"
	}
	return fmt.Errorf("%s at %q", fmt.Sprintf(format, args...), at)
}

func appendPathKey(base, key string) string {
	if base == "" {
		return key
	}
	if strings.ContainsAny(key, ".[]\"'") {
		return base + "[" + strconv.Quote(key) + "]"
	}
	return base + "." + key
}

func isReadOnlySQL(query string) bool {
	return validateReadOnlySQL(query) == nil
}

func validateReadOnlySQL(query string) error {
	q := strings.TrimSpace(query)
	if q == "" {
		return fmt.Errorf("query is required")
	}
	if hasMultipleSQLStatements(q) {
		return fmt.Errorf("only one SQL statement is allowed")
	}
	q = strings.TrimSpace(strings.TrimSuffix(q, ";"))
	lower := strings.ToLower(q)
	switch {
	case strings.HasPrefix(lower, "select "):
		return nil
	case strings.HasPrefix(lower, "with "):
		if containsSQLWriteKeyword(lower) {
			return fmt.Errorf("WITH query contains a non-read-only keyword")
		}
		return nil
	case strings.HasPrefix(lower, "pragma "):
		if !isAllowedReadOnlyPragma(lower) {
			return fmt.Errorf("pragma is not allowlisted for read-only sql_query")
		}
		return nil
	case strings.HasPrefix(lower, "explain "):
		return validateExplainSQL(q)
	default:
		return fmt.Errorf("only read-only queries are allowed")
	}
}

func hasMultipleSQLStatements(query string) bool {
	inSingle := false
	inDouble := false
	inLineComment := false
	inBlockComment := false
	for i := 0; i < len(query); i++ {
		ch := query[i]
		next := byte(0)
		if i+1 < len(query) {
			next = query[i+1]
		}
		if inLineComment {
			if ch == '\n' {
				inLineComment = false
			}
			continue
		}
		if inBlockComment {
			if ch == '*' && next == '/' {
				inBlockComment = false
				i++
			}
			continue
		}
		if inSingle {
			if ch == '\'' {
				if next == '\'' {
					i++
					continue
				}
				inSingle = false
			}
			continue
		}
		if inDouble {
			if ch == '"' {
				if next == '"' {
					i++
					continue
				}
				inDouble = false
			}
			continue
		}
		switch {
		case ch == '-' && next == '-':
			inLineComment = true
			i++
		case ch == '/' && next == '*':
			inBlockComment = true
			i++
		case ch == '\'':
			inSingle = true
		case ch == '"':
			inDouble = true
		case ch == ';':
			if strings.TrimSpace(query[i+1:]) != "" {
				return true
			}
		}
	}
	return false
}

func containsSQLWriteKeyword(lower string) bool {
	writeKeywords := []string{"insert", "update", "delete", "replace", "create", "drop", "alter", "attach", "detach", "vacuum", "reindex"}
	for _, keyword := range writeKeywords {
		if regexp.MustCompile(`\b` + keyword + `\b`).MatchString(lower) {
			return true
		}
	}
	return false
}

func isAllowedReadOnlyPragma(lower string) bool {
	body := strings.TrimSpace(strings.TrimPrefix(lower, "pragma"))
	body = strings.TrimSpace(strings.TrimSuffix(body, ";"))
	for _, sep := range []string{"(", "=", " "} {
		if idx := strings.Index(body, sep); idx >= 0 {
			body = body[:idx]
		}
	}
	body = strings.TrimSpace(body)
	if dot := strings.LastIndex(body, "."); dot >= 0 {
		body = body[dot+1:]
	}
	switch body {
	case "table_info", "table_xinfo", "index_list", "index_info", "index_xinfo", "foreign_key_list", "database_list", "integrity_check", "quick_check":
		return true
	default:
		return false
	}
}

func validateExplainSQL(query string) error {
	rest := strings.TrimSpace(query[len("explain "):])
	lower := strings.ToLower(rest)
	if strings.HasPrefix(lower, "query plan ") {
		rest = strings.TrimSpace(rest[len("query plan "):])
	}
	if err := validateReadOnlySQL(rest); err != nil {
		return fmt.Errorf("EXPLAIN target is not read-only: %w", err)
	}
	return nil
}

func scanSQLRows(rows *sql.Rows, limit int) ([]map[string]any, []string, bool, error) {
	columns, err := rows.Columns()
	if err != nil {
		return nil, nil, false, fmt.Errorf("read columns: %w", err)
	}
	result := make([]map[string]any, 0, limit)
	truncated := false
	for rows.Next() {
		if len(result) >= limit {
			truncated = true
			break
		}
		values := make([]any, len(columns))
		dest := make([]any, len(columns))
		for i := range values {
			dest[i] = &values[i]
		}
		if err := rows.Scan(dest...); err != nil {
			return nil, nil, false, fmt.Errorf("scan row: %w", err)
		}
		entry := make(map[string]any, len(columns))
		for i, col := range columns {
			entry[col] = normalizeSQLValue(values[i])
		}
		result = append(result, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, false, fmt.Errorf("iterate rows: %w", err)
	}
	return result, columns, truncated, nil
}

func normalizeSQLValue(v any) any {
	switch x := v.(type) {
	case []byte:
		if utf8.Valid(x) {
			return string(x)
		}
		return map[string]any{
			"type":   "blob",
			"bytes":  len(x),
			"base64": base64.StdEncoding.EncodeToString(x),
		}
	default:
		return x
	}
}

func sqliteReadOnlyDSN(path string) string {
	u := url.URL{Scheme: "file", Path: path}
	q := u.Query()
	q.Set("mode", "ro")
	u.RawQuery = q.Encode()
	return u.String()
}

func sqliteTableSchema(db *sql.DB, table string) ([]map[string]any, error) {
	rows, err := db.Query(fmt.Sprintf("PRAGMA table_info(%s)", sqliteQuoteIdentifier(table)))
	if err != nil {
		return nil, fmt.Errorf("describe table %s: %w", table, err)
	}
	defer rows.Close()

	var cols []map[string]any
	for rows.Next() {
		var cid int
		var name, colType string
		var notNull, pk int
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &colType, &notNull, &dflt, &pk); err != nil {
			return nil, fmt.Errorf("scan schema row: %w", err)
		}
		var defaultValue any
		if dflt.Valid {
			defaultValue = dflt.String
		}
		cols = append(cols, map[string]any{
			"cid":         cid,
			"name":        name,
			"type":        colType,
			"not_null":    notNull == 1,
			"default":     defaultValue,
			"has_default": dflt.Valid,
			"primary":     pk == 1,
		})
	}
	if len(cols) == 0 {
		return nil, fmt.Errorf("table %q not found or has no visible columns", table)
	}
	return cols, nil
}

func dbSchemaIncludes(raw any) (map[string]bool, error) {
	values, err := stringListArg(raw)
	if err != nil {
		return nil, fmt.Errorf("include: %w", err)
	}
	if len(values) == 0 {
		values = []string{"columns"}
	}
	include := make(map[string]bool, len(values))
	for _, value := range values {
		value = strings.ToLower(strings.TrimSpace(value))
		switch value {
		case "columns", "indexes", "foreign_keys", "views", "triggers":
			include[value] = true
		default:
			return nil, fmt.Errorf("unsupported include section %q", value)
		}
	}
	return include, nil
}

func sqliteTableDetails(db *sql.DB, table string, include map[string]bool) (map[string]any, error) {
	result := make(map[string]any)
	if include["columns"] {
		cols, err := sqliteTableSchema(db, table)
		if err != nil {
			return nil, err
		}
		result["columns"] = cols
	} else if err := sqliteTableExists(db, table); err != nil {
		return nil, err
	}
	if include["indexes"] {
		indexes, err := sqliteTableIndexes(db, table)
		if err != nil {
			return nil, err
		}
		result["indexes"] = indexes
	}
	if include["foreign_keys"] {
		foreignKeys, err := sqliteTableForeignKeys(db, table)
		if err != nil {
			return nil, err
		}
		result["foreign_keys"] = foreignKeys
	}
	return result, nil
}

func sqliteTableExists(db *sql.DB, table string) error {
	var name string
	err := db.QueryRow(`SELECT name FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&name)
	if err == sql.ErrNoRows {
		return fmt.Errorf("table %q not found or has no visible columns", table)
	}
	if err != nil {
		return fmt.Errorf("check table %s: %w", table, err)
	}
	return nil
}

func sqliteTableIndexes(db *sql.DB, table string) ([]map[string]any, error) {
	rows, err := db.Query(fmt.Sprintf("PRAGMA index_list(%s)", sqliteQuoteIdentifier(table)))
	if err != nil {
		return nil, fmt.Errorf("list indexes for %s: %w", table, err)
	}
	defer rows.Close()

	var indexes []map[string]any
	for rows.Next() {
		var seq int
		var name string
		var unique int
		var origin string
		var partial int
		if err := rows.Scan(&seq, &name, &unique, &origin, &partial); err != nil {
			return nil, fmt.Errorf("scan index list: %w", err)
		}
		columns, err := sqliteIndexColumns(db, name)
		if err != nil {
			return nil, err
		}
		indexes = append(indexes, map[string]any{
			"seq":     seq,
			"name":    name,
			"unique":  unique == 1,
			"origin":  origin,
			"partial": partial == 1,
			"columns": columns,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate index list: %w", err)
	}
	return indexes, nil
}

func sqliteIndexColumns(db *sql.DB, indexName string) ([]string, error) {
	rows, err := db.Query(fmt.Sprintf("PRAGMA index_info(%s)", sqliteQuoteIdentifier(indexName)))
	if err != nil {
		return nil, fmt.Errorf("read index %s columns: %w", indexName, err)
	}
	defer rows.Close()

	var columns []string
	for rows.Next() {
		var seqno, cid int
		var name string
		if err := rows.Scan(&seqno, &cid, &name); err != nil {
			return nil, fmt.Errorf("scan index columns: %w", err)
		}
		columns = append(columns, name)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate index columns: %w", err)
	}
	return columns, nil
}

func sqliteTableForeignKeys(db *sql.DB, table string) ([]map[string]any, error) {
	rows, err := db.Query(fmt.Sprintf("PRAGMA foreign_key_list(%s)", sqliteQuoteIdentifier(table)))
	if err != nil {
		return nil, fmt.Errorf("list foreign keys for %s: %w", table, err)
	}
	defer rows.Close()

	var out []map[string]any
	for rows.Next() {
		var id, seq int
		var refTable, from, to, onUpdate, onDelete, match string
		if err := rows.Scan(&id, &seq, &refTable, &from, &to, &onUpdate, &onDelete, &match); err != nil {
			return nil, fmt.Errorf("scan foreign key: %w", err)
		}
		out = append(out, map[string]any{
			"id":        id,
			"seq":       seq,
			"table":     refTable,
			"from":      from,
			"to":        to,
			"on_update": onUpdate,
			"on_delete": onDelete,
			"match":     match,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate foreign keys: %w", err)
	}
	return out, nil
}

func sqliteObjects(db *sql.DB, objectType, table string, includeInternal, includeSQL bool, limit int) ([]map[string]any, error) {
	query := `SELECT name, tbl_name, sql FROM sqlite_master WHERE type=?`
	args := []any{objectType}
	if strings.TrimSpace(table) != "" {
		query += ` AND tbl_name=?`
		args = append(args, table)
	}
	if !includeInternal {
		query += ` AND name NOT LIKE 'sqlite_%'`
	}
	query += ` ORDER BY name`
	if limit > 0 {
		query += ` LIMIT ?`
		args = append(args, limit)
	}
	rows, err := db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("list sqlite %s objects: %w", objectType, err)
	}
	defer rows.Close()

	var out []map[string]any
	for rows.Next() {
		var name, tblName string
		var sqlText sql.NullString
		if err := rows.Scan(&name, &tblName, &sqlText); err != nil {
			return nil, fmt.Errorf("scan sqlite %s object: %w", objectType, err)
		}
		item := map[string]any{
			"name":  name,
			"table": tblName,
		}
		if includeSQL && sqlText.Valid {
			item["sql"] = sqlText.String
		}
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate sqlite %s objects: %w", objectType, err)
	}
	return out, nil
}

func sqliteQuoteIdentifier(name string) string {
	return `"` + strings.ReplaceAll(strings.TrimSpace(name), `"`, `""`) + `"`
}
