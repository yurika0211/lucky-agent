// Lexical match score shared by search and temporal candidate selection.
package memory

import "strings"

func memoryMatchScore(e *Entry, queryLower string, queryTerms []string) float64 {
	if e == nil {
		return 0
	}
	contentLower := strings.ToLower(e.Content)
	categoryLower := strings.ToLower(e.Category)

	matchScore := 0.0
	if queryLower != "" && strings.Contains(contentLower, queryLower) {
		matchScore = 1.0
		if contentLower == queryLower {
			matchScore = 2.0
		}
	}
	if queryLower != "" && strings.Contains(categoryLower, queryLower) {
		matchScore += 0.5
	}

	termHits := 0
	for _, term := range queryTerms {
		if term == "" {
			continue
		}
		if strings.Contains(contentLower, term) {
			matchScore += 0.22
			termHits++
			continue
		}
		if strings.Contains(categoryLower, term) {
			matchScore += 0.12
			termHits++
		}
	}
	if termHits >= 2 {
		matchScore += 0.25
	}

	for _, tag := range e.Tags {
		tagLower := strings.ToLower(tag)
		if queryLower != "" && strings.Contains(tagLower, queryLower) {
			matchScore += 0.3
			break
		}
		for _, term := range queryTerms {
			if strings.Contains(tagLower, term) {
				matchScore += 0.12
				break
			}
		}
	}
	for _, alias := range e.Aliases {
		aliasLower := strings.ToLower(alias)
		if queryLower != "" && (strings.Contains(aliasLower, queryLower) || strings.Contains(queryLower, aliasLower)) {
			matchScore += 0.5
			break
		}
		for _, term := range queryTerms {
			if strings.Contains(aliasLower, term) || strings.Contains(term, aliasLower) {
				matchScore += 0.16
				break
			}
		}
	}
	for _, link := range e.Links {
		linkLower := strings.ToLower(link)
		if queryLower != "" && (strings.Contains(linkLower, queryLower) || strings.Contains(queryLower, linkLower)) {
			matchScore += 0.6
			break
		}
		for _, term := range queryTerms {
			if strings.Contains(linkLower, term) || strings.Contains(term, linkLower) {
				matchScore += 0.18
				break
			}
		}
	}
	return matchScore
}
