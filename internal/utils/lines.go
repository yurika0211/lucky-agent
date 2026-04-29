package utils

/*
SplitLines 按 '\n' 拆分文本，并移除每一行尾部可能存在的 '\r'。
*/
func SplitLines(s string) []string {
	var lines []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			lines = append(lines, trimTrailingCR(s[start:i]))
			start = i + 1
		}
	}
	if start < len(s) {
		lines = append(lines, trimTrailingCR(s[start:]))
	}
	return lines
}

/*
SplitLinesBytes 按 '\n' 拆分字节内容，并移除每一行尾部可能存在的 '\r'。
*/
func SplitLinesBytes(data []byte) []string {
	var lines []string
	start := 0
	for i, b := range data {
		if b == '\n' {
			lines = append(lines, trimTrailingCR(string(data[start:i])))
			start = i + 1
		}
	}
	if start < len(data) {
		lines = append(lines, trimTrailingCR(string(data[start:])))
	}
	return lines
}

/*
trimTrailingCR 去掉字符串末尾单个回车符 '\r'。
*/
func trimTrailingCR(s string) string {
	if len(s) > 0 && s[len(s)-1] == '\r' {
		return s[:len(s)-1]
	}
	return s
}
