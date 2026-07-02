package tool

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

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
			"url":          {Type: "string", Description: "HTTP or HTTPS URL to request.", Required: true},
			"method":       {Type: "string", Description: "HTTP method such as GET, POST, PUT, PATCH, DELETE. Default GET.", Required: false, Default: "GET"},
			"headers_json": {Type: "string", Description: "Optional JSON object of request headers.", Required: false},
			"body":         {Type: "string", Description: "Optional request body.", Required: false},
			"timeout":      {Type: "number", Description: "Timeout in seconds (default 15, max 60).", Required: false, Default: 15},
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
	timeout := boundedIntArg(args, "timeout", 15, 1, 60)
	body, _ := args["body"].(string)

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
			req.Header.Set(k, v)
		}
	}

	client := &http.Client{Timeout: time.Duration(timeout) * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("send request: %w", err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(io.LimitReader(resp.Body, 32*1024))
	if err != nil {
		return "", fmt.Errorf("read response: %w", err)
	}
	bodyText := strings.TrimSpace(string(data))
	if json.Valid(data) {
		var pretty bytes.Buffer
		if err := json.Indent(&pretty, data, "", "  "); err == nil {
			bodyText = pretty.String()
		}
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf("Status: %s\n", resp.Status))
	if ct := strings.TrimSpace(resp.Header.Get("Content-Type")); ct != "" {
		b.WriteString("Content-Type: " + ct + "\n")
	}
	if bodyText != "" {
		b.WriteString("\n")
		b.WriteString(utils.Truncate(bodyText, 12000))
	}
	return strings.TrimSpace(b.String()), nil
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
			"path":  {Type: "string", Description: "Path to the JSON file.", Required: true},
			"query": {Type: "string", Description: "Dot-path query such as user.name or items[0].id. Leave empty to pretty-print the full document.", Required: false},
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
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read json file: %w", err)
	}
	var doc any
	if err := json.Unmarshal(data, &doc); err != nil {
		return "", fmt.Errorf("parse json: %w", err)
	}
	query, _ := args["query"].(string)
	return queryStructuredValue(doc, query)
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
			"path":  {Type: "string", Description: "Path to the YAML file.", Required: true},
			"query": {Type: "string", Description: "Dot-path query such as metadata.name or items[0].id. Leave empty to pretty-print the full document.", Required: false},
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
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read yaml file: %w", err)
	}
	var doc any
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return "", fmt.Errorf("parse yaml: %w", err)
	}
	return queryStructuredValue(normalizeYAMLValue(doc), args["query"])
}

// CSVQueryTool returns rows from a CSV file, optionally filtered by one column.
func CSVQueryTool() *Tool {
	return &Tool{
		Name:        "csv_query",
		Description: "Read a CSV file and optionally filter rows by one column equality match.",
		Category:    CatBuiltin,
		Source:      "builtin",
		Permission:  PermAuto,
		Parameters: map[string]Param{
			"path":   {Type: "string", Description: "Path to the CSV file.", Required: true},
			"column": {Type: "string", Description: "Optional column name to filter or project.", Required: false},
			"equals": {Type: "string", Description: "Optional exact string to match in the chosen column.", Required: false},
			"limit":  {Type: "number", Description: "Maximum number of rows to return (default 20, max 100).", Required: false, Default: 20},
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
	column, _ := args["column"].(string)
	equals, _ := args["equals"].(string)

	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("open csv file: %w", err)
	}
	defer f.Close()

	reader := csv.NewReader(f)
	rows, err := reader.ReadAll()
	if err != nil {
		return "", fmt.Errorf("read csv: %w", err)
	}
	if len(rows) == 0 {
		return "", fmt.Errorf("csv is empty")
	}
	headers := rows[0]
	colIdx := -1
	if strings.TrimSpace(column) != "" {
		for i, h := range headers {
			if h == column {
				colIdx = i
				break
			}
		}
		if colIdx < 0 {
			return "", fmt.Errorf("column %q not found", column)
		}
	}

	var out []map[string]string
	for _, row := range rows[1:] {
		if colIdx >= 0 && strings.TrimSpace(equals) != "" {
			if colIdx >= len(row) || row[colIdx] != equals {
				continue
			}
		}
		entry := make(map[string]string, len(headers))
		for i, h := range headers {
			if i < len(row) {
				entry[h] = row[i]
			} else {
				entry[h] = ""
			}
		}
		out = append(out, entry)
		if len(out) >= limit {
			break
		}
	}
	return prettyStructuredValue(out)
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
			"path":  {Type: "string", Description: "Path to the SQLite database file.", Required: true},
			"query": {Type: "string", Description: "Read-only SQL query (SELECT, WITH, PRAGMA, EXPLAIN).", Required: true},
			"limit": {Type: "number", Description: "Maximum number of rows to return (default 50, max 200).", Required: false, Default: 50},
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
	if !isReadOnlySQL(query) {
		return "", fmt.Errorf("only read-only queries are allowed")
	}
	limit := boundedIntArg(args, "limit", 50, 1, 200)

	db, err := sql.Open("sqlite3", path)
	if err != nil {
		return "", fmt.Errorf("open sqlite database: %w", err)
	}
	defer db.Close()

	rows, err := db.Query(query)
	if err != nil {
		return "", fmt.Errorf("query sqlite database: %w", err)
	}
	defer rows.Close()

	result, err := scanSQLRows(rows, limit)
	if err != nil {
		return "", err
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
			"path":  {Type: "string", Description: "Path to the SQLite database file.", Required: true},
			"table": {Type: "string", Description: "Optional specific table name.", Required: false},
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

	db, err := sql.Open("sqlite3", path)
	if err != nil {
		return "", fmt.Errorf("open sqlite database: %w", err)
	}
	defer db.Close()

	if strings.TrimSpace(table) != "" {
		cols, err := sqliteTableSchema(db, table)
		if err != nil {
			return "", err
		}
		return prettyStructuredValue(map[string]any{
			"table":   table,
			"columns": cols,
		})
	}

	rows, err := db.Query(`SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%' ORDER BY name`)
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
		cols, err := sqliteTableSchema(db, name)
		if err != nil {
			return "", err
		}
		tables = append(tables, map[string]any{
			"name":    name,
			"columns": cols,
		})
	}
	return prettyStructuredValue(map[string]any{
		"tables": tables,
	})
}

func queryStructuredValue(doc any, query any) (string, error) {
	queryText, _ := query.(string)
	queryText = strings.TrimSpace(queryText)
	if queryText == "" {
		return prettyStructuredValue(doc)
	}
	value, err := walkStructuredPath(doc, queryText)
	if err != nil {
		return "", err
	}
	return prettyStructuredValue(value)
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
	current := doc
	for _, token := range parseStructuredPath(query) {
		if token.key != "" {
			obj, ok := current.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("path %q expected object", token.key)
			}
			next, ok := obj[token.key]
			if !ok {
				return nil, fmt.Errorf("path key %q not found", token.key)
			}
			current = next
		}
		if token.hasIndex {
			arr, ok := current.([]any)
			if !ok {
				return nil, fmt.Errorf("path index %d expected array", token.index)
			}
			if token.index < 0 || token.index >= len(arr) {
				return nil, fmt.Errorf("path index %d out of range", token.index)
			}
			current = arr[token.index]
		}
	}
	return current, nil
}

type structuredPathToken struct {
	key      string
	index    int
	hasIndex bool
}

func parseStructuredPath(query string) []structuredPathToken {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil
	}
	parts := strings.Split(query, ".")
	out := make([]structuredPathToken, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		token := structuredPathToken{}
		if idx := strings.Index(part, "["); idx >= 0 && strings.HasSuffix(part, "]") {
			token.key = part[:idx]
			rawIndex := part[idx+1 : len(part)-1]
			if n, err := strconv.Atoi(rawIndex); err == nil {
				token.index = n
				token.hasIndex = true
			}
		} else {
			token.key = part
		}
		out = append(out, token)
	}
	return out
}

func isReadOnlySQL(query string) bool {
	q := strings.TrimSpace(strings.ToLower(query))
	switch {
	case strings.HasPrefix(q, "select "),
		strings.HasPrefix(q, "with "),
		strings.HasPrefix(q, "pragma "),
		strings.HasPrefix(q, "explain "):
		return true
	default:
		return false
	}
}

func scanSQLRows(rows *sql.Rows, limit int) ([]map[string]any, error) {
	columns, err := rows.Columns()
	if err != nil {
		return nil, fmt.Errorf("read columns: %w", err)
	}
	result := make([]map[string]any, 0, limit)
	for rows.Next() {
		values := make([]any, len(columns))
		dest := make([]any, len(columns))
		for i := range values {
			dest[i] = &values[i]
		}
		if err := rows.Scan(dest...); err != nil {
			return nil, fmt.Errorf("scan row: %w", err)
		}
		entry := make(map[string]any, len(columns))
		for i, col := range columns {
			entry[col] = normalizeSQLValue(values[i])
		}
		result = append(result, entry)
		if len(result) >= limit {
			break
		}
	}
	return result, nil
}

func normalizeSQLValue(v any) any {
	switch x := v.(type) {
	case []byte:
		return string(x)
	default:
		return x
	}
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
		cols = append(cols, map[string]any{
			"cid":      cid,
			"name":     name,
			"type":     colType,
			"not_null": notNull == 1,
			"default":  dflt.String,
			"primary":  pk == 1,
		})
	}
	if len(cols) == 0 {
		return nil, fmt.Errorf("table %q not found or has no visible columns", table)
	}
	return cols, nil
}

func sqliteQuoteIdentifier(name string) string {
	return `"` + strings.ReplaceAll(strings.TrimSpace(name), `"`, `""`) + `"`
}
