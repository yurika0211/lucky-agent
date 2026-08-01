package agent

import "strings"

type shellCommandFinding struct {
	Writes  bool
	Deletes bool
	Pushes  bool
	Reason  string
}

type shellToken struct {
	Text   string
	Quoted bool
}

func classifyShellCommand(command string) shellCommandFinding {
	tokens := tokenizeShellCommand(command)
	for i, token := range tokens {
		word := strings.ToLower(token.Text)
		if word == "" || token.Quoted {
			continue
		}
		if word == "git" && nextShellWord(tokens, i) == "push" {
			return shellCommandFinding{Pushes: true, Reason: "git push"}
		}
	}
	for i, token := range tokens {
		word := strings.ToLower(token.Text)
		if word == "" || token.Quoted {
			continue
		}
		switch word {
		case "rm", "rmdir", "unlink", "del":
			return shellCommandFinding{Deletes: true, Reason: word}
		}
		if isShellRedirect(word) {
			return shellCommandFinding{Writes: true, Reason: "redirection"}
		}
		if word == "sed" && hasShellFlag(tokens, i+1, "-i") {
			return shellCommandFinding{Writes: true, Reason: "sed -i"}
		}
		if word == "perl" && hasShellFlag(tokens, i+1, "-pi") {
			return shellCommandFinding{Writes: true, Reason: "perl -pi"}
		}
		if word == "git" {
			next := nextShellWord(tokens, i)
			if next == "add" || next == "commit" {
				return shellCommandFinding{Writes: true, Reason: "git " + next}
			}
		}
		switch word {
		case "tee", "touch", "mv", "cp", "chmod", "chown", "truncate", "dd", "install":
			return shellCommandFinding{Writes: true, Reason: word}
		}
	}
	return shellCommandFinding{}
}

func tokenizeShellCommand(command string) []shellToken {
	var tokens []shellToken
	var b strings.Builder
	quote := rune(0)
	quoted := false
	flush := func() {
		if b.Len() == 0 {
			quoted = false
			return
		}
		tokens = append(tokens, shellToken{Text: b.String(), Quoted: quoted})
		b.Reset()
		quoted = false
	}
	for _, r := range command {
		if quote != 0 {
			if r == quote {
				quote = 0
				quoted = true
				continue
			}
			b.WriteRune(r)
			continue
		}
		switch r {
		case '\'', '"':
			if b.Len() == 0 {
				quote = r
				quoted = true
				continue
			}
			b.WriteRune(r)
		case ' ', '\t', '\n', '\r', ';', '|', '&', '(', ')':
			flush()
		case '>':
			flush()
			tokens = append(tokens, shellToken{Text: ">"})
		default:
			b.WriteRune(r)
		}
	}
	flush()
	return tokens
}

func nextShellWord(tokens []shellToken, start int) string {
	for i := start + 1; i < len(tokens); i++ {
		if tokens[i].Quoted || tokens[i].Text == "" {
			continue
		}
		return strings.ToLower(tokens[i].Text)
	}
	return ""
}

func hasShellFlag(tokens []shellToken, start int, flag string) bool {
	for i := start; i < len(tokens); i++ {
		if tokens[i].Quoted {
			continue
		}
		word := strings.ToLower(tokens[i].Text)
		if word == "" {
			continue
		}
		if word == flag || strings.HasPrefix(word, flag) {
			return true
		}
		if !strings.HasPrefix(word, "-") {
			return false
		}
	}
	return false
}

func isShellRedirect(word string) bool {
	if word == ">" || word == ">>" || word == "&>" {
		return true
	}
	return strings.HasSuffix(word, ">") || strings.HasSuffix(word, ">>")
}
