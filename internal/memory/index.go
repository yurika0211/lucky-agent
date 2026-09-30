// Derived indexes for duplicate detection and lexical candidate lookup.
// Markdown notes remain the source of truth. Both indexes are rebuilt from
// loaded notes and updated when a note changes.
package memory

import (
	"hash/fnv"
	"strings"
	"unicode"
	"unicode/utf8"
)

func (s *Store) rebuildDerivedLocked() {
	s.rebuildGraphLocked()
	s.rebuildLexicalLocked()
}

func (s *Store) indexEntryLocked(e *Entry) {
	if s == nil || e == nil || e.ID == "" {
		return
	}
	s.indexGraphEntryLocked(e)
	s.indexLexicalEntryLocked(e)
}

func (s *Store) unindexEntryLocked(e *Entry) {
	if s == nil || e == nil || e.ID == "" {
		return
	}
	s.unindexGraphEntryLocked(e)
	s.unindexLexicalEntryLocked(e)
}

func duplicateKey(content, category string) string {
	return strings.ToLower(strings.TrimSpace(category)) + "\n" + strings.ToLower(strings.TrimSpace(content))
}

func (s *Store) rememberDuplicateLocked(e *Entry) {
	if s == nil || e == nil || e.ID == "" {
		return
	}
	if s.duplicates == nil {
		s.duplicates = make(map[string]string)
	}
	s.duplicates[duplicateKey(e.Content, e.Category)] = e.ID
}

func (s *Store) forgetDuplicateLocked(e *Entry) {
	if s == nil || e == nil || s.duplicates == nil {
		return
	}
	key := duplicateKey(e.Content, e.Category)
	if s.duplicates[key] == e.ID {
		delete(s.duplicates, key)
	}
}

func (s *Store) duplicateOfLocked(content, category string) *Entry {
	if s == nil || s.duplicates == nil {
		return nil
	}
	id := s.duplicates[duplicateKey(content, category)]
	if id == "" {
		return nil
	}
	return s.entries[id]
}

type lexicalPosting struct {
	entries map[string]struct{}
}

func (s *Store) rebuildLexicalLocked() {
	index := newLexicalIndex()
	duplicates := make(map[string]string, len(s.entries))
	for _, entry := range s.entries {
		if entry == nil {
			continue
		}
		index.add(entry)
		duplicates[duplicateKey(entry.Content, entry.Category)] = entry.ID
	}
	s.lexical = index
	s.duplicates = duplicates
}

func (s *Store) indexLexicalEntryLocked(e *Entry) {
	if s.lexical == nil {
		s.lexical = newLexicalIndex()
	}
	s.lexical.add(e)
	s.rememberDuplicateLocked(e)
}

func (s *Store) unindexLexicalEntryLocked(e *Entry) {
	if s.lexical != nil {
		s.lexical.remove(e)
	}
	s.forgetDuplicateLocked(e)
}

func newLexicalIndex() *lexicalIndex {
	return &lexicalIndex{
		terms:    make(map[string]*lexicalPosting),
		grams:    make(map[uint64]*lexicalPosting),
		phrases:  make(map[string]*lexicalPosting),
		exact:    make(map[string]*lexicalPosting),
		category: make(map[string]*lexicalPosting),
	}
}

// lexicalIndex finds notes that can score above zero for a query.
//
// Term and category postings cover ordinary token hits. Content grams cover
// the same substring hits as strings.Contains, including matches that cross a
// token boundary. Phrase postings cover tags, aliases, and wikilinks.
type lexicalIndex struct {
	terms    map[string]*lexicalPosting
	grams    map[uint64]*lexicalPosting
	phrases  map[string]*lexicalPosting
	exact    map[string]*lexicalPosting
	category map[string]*lexicalPosting
}

func (idx *lexicalIndex) add(e *Entry) {
	if idx == nil || e == nil || e.ID == "" {
		return
	}
	content := strings.ToLower(e.Content)
	category := strings.ToLower(e.Category)
	for _, term := range indexTerms(content) {
		idx.addTerm(term, e.ID)
	}
	for _, term := range indexTerms(category) {
		idx.addCategory(term, e.ID)
	}
	for _, gram := range contentGrams(content) {
		idx.addGram(gram, e.ID)
	}
	if content != "" {
		idx.addExact(content, e.ID)
	}
	if category != "" {
		idx.addPhrase(category, e.ID)
	}
	for _, tag := range e.Tags {
		idx.addPhrase(strings.ToLower(tag), e.ID)
	}
	for _, alias := range e.Aliases {
		idx.addPhrase(strings.ToLower(alias), e.ID)
	}
	for _, link := range e.Links {
		idx.addPhrase(strings.ToLower(link), e.ID)
	}
}

func (idx *lexicalIndex) remove(e *Entry) {
	if idx == nil || e == nil || e.ID == "" {
		return
	}
	content := strings.ToLower(e.Content)
	category := strings.ToLower(e.Category)
	for _, term := range indexTerms(content) {
		idx.removeTerm(term, e.ID)
	}
	for _, term := range indexTerms(category) {
		idx.removeCategory(term, e.ID)
	}
	for _, gram := range contentGrams(content) {
		idx.removeGram(gram, e.ID)
	}
	if content != "" {
		idx.removeExact(content, e.ID)
	}
	if category != "" {
		idx.removePhrase(category, e.ID)
	}
	for _, tag := range e.Tags {
		idx.removePhrase(strings.ToLower(tag), e.ID)
	}
	for _, alias := range e.Aliases {
		idx.removePhrase(strings.ToLower(alias), e.ID)
	}
	for _, link := range e.Links {
		idx.removePhrase(strings.ToLower(link), e.ID)
	}
}

func (idx *lexicalIndex) candidateIDs(queryLower string, queryTerms []string) map[string]struct{} {
	if idx == nil {
		return nil
	}
	ids := make(map[string]struct{})
	addPosting := func(posting *lexicalPosting) {
		if posting == nil {
			return
		}
		for id := range posting.entries {
			ids[id] = struct{}{}
		}
	}
	if queryLower != "" {
		addPosting(idx.exact[queryLower])
		addPosting(idx.phrases[queryLower])
		for _, gram := range contentGrams(queryLower) {
			addPosting(idx.grams[gram])
		}
		for _, term := range indexTerms(queryLower) {
			addPosting(idx.terms[term])
			addPosting(idx.category[term])
		}
	}
	for _, term := range queryTerms {
		term = strings.ToLower(strings.TrimSpace(term))
		if term == "" {
			continue
		}
		addPosting(idx.terms[term])
		addPosting(idx.category[term])
		addPosting(idx.phrases[term])
		for _, gram := range contentGrams(term) {
			addPosting(idx.grams[gram])
		}
	}
	return ids
}

func (idx *lexicalIndex) addTerm(term, id string) {
	idx.addPosting(idx.terms, term, id)
}

func (idx *lexicalIndex) removeTerm(term, id string) {
	idx.removePosting(idx.terms, term, id)
}

func (idx *lexicalIndex) addCategory(term, id string) {
	idx.addPosting(idx.category, term, id)
}

func (idx *lexicalIndex) removeCategory(term, id string) {
	idx.removePosting(idx.category, term, id)
}

func (idx *lexicalIndex) addPhrase(phrase, id string) {
	idx.addPosting(idx.phrases, phrase, id)
}

func (idx *lexicalIndex) removePhrase(phrase, id string) {
	idx.removePosting(idx.phrases, phrase, id)
}

func (idx *lexicalIndex) addExact(content, id string) {
	idx.addPosting(idx.exact, content, id)
}

func (idx *lexicalIndex) removeExact(content, id string) {
	idx.removePosting(idx.exact, content, id)
}

func (idx *lexicalIndex) addGram(gram uint64, id string) {
	if gram == 0 || id == "" {
		return
	}
	posting := idx.grams[gram]
	if posting == nil {
		posting = &lexicalPosting{entries: make(map[string]struct{})}
		idx.grams[gram] = posting
	}
	posting.entries[id] = struct{}{}
}

func (idx *lexicalIndex) removeGram(gram uint64, id string) {
	posting := idx.grams[gram]
	if posting == nil {
		return
	}
	delete(posting.entries, id)
	if len(posting.entries) == 0 {
		delete(idx.grams, gram)
	}
}

func (idx *lexicalIndex) addPosting(table map[string]*lexicalPosting, key, id string) {
	key = strings.TrimSpace(key)
	if key == "" || id == "" {
		return
	}
	posting := table[key]
	if posting == nil {
		posting = &lexicalPosting{entries: make(map[string]struct{})}
		table[key] = posting
	}
	posting.entries[id] = struct{}{}
}

func (idx *lexicalIndex) removePosting(table map[string]*lexicalPosting, key, id string) {
	key = strings.TrimSpace(key)
	posting := table[key]
	if posting == nil {
		return
	}
	delete(posting.entries, id)
	if len(posting.entries) == 0 {
		delete(table, key)
	}
}

// indexTerms returns the tokens stored in the inverted index. Unlike query
// expansion, this does not add aliases. A query alias still finds the note
// because the alias text is what gets looked up.
func indexTerms(text string) []string {
	var terms []string
	var latin strings.Builder
	var han []rune

	flushLatin := func() {
		if latin.Len() == 0 {
			return
		}
		token := strings.ToLower(latin.String())
		if utf8.RuneCountInString(token) >= 2 {
			terms = append(terms, token)
		}
		latin.Reset()
	}
	flushHan := func() {
		if len(han) == 0 {
			return
		}
		if len(han) == 1 {
			han = han[:0]
			return
		}
		if len(han) <= 4 {
			terms = append(terms, string(han))
		}
		for n := 2; n <= 4; n++ {
			if len(han) < n {
				continue
			}
			for i := 0; i+n <= len(han); i++ {
				terms = append(terms, string(han[i:i+n]))
			}
		}
		han = han[:0]
	}

	for _, r := range text {
		switch {
		case unicode.Is(unicode.Han, r):
			flushLatin()
			han = append(han, r)
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			flushHan()
			latin.WriteRune(unicode.ToLower(r))
		default:
			flushLatin()
			flushHan()
		}
	}
	flushLatin()
	flushHan()
	return dedupSlice(terms)
}

const contentGramRunes = 2

func contentGrams(text string) []uint64 {
	runes := []rune(text)
	if len(runes) < contentGramRunes {
		return nil
	}
	grams := make([]uint64, 0, len(runes)-contentGramRunes+1)
	for i := 0; i+contentGramRunes <= len(runes); i++ {
		grams = append(grams, gramHash(runes[i:i+contentGramRunes]))
	}
	return grams
}

func gramHash(runes []rune) uint64 {
	h := fnv.New64a()
	var buf [4]byte
	for _, r := range runes {
		n := utf8.EncodeRune(buf[:], r)
		_, _ = h.Write(buf[:n])
	}
	return h.Sum64()
}
