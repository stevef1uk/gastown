package specprofile

import (
	"math"
	"regexp"
	"strings"
)

type Chunk struct {
	Heading string
	Content string
	Source  string
}

var headingPat = regexp.MustCompile(`(?m)^(#{2,4})\s+(.+)$`)

func ChunkDocument(text, sourceLabel string) []Chunk {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}

	lines := strings.Split(text, "\n")
	var chunks []Chunk
	var curHeading string
	var curContent []string

	flush := func() {
		if len(curContent) > 0 || curHeading != "" {
			body := strings.TrimSpace(strings.Join(curContent, "\n"))
			if body != "" || curHeading != "" {
				chunks = append(chunks, Chunk{
					Heading: curHeading,
					Content: body,
					Source:  sourceLabel,
				})
			}
		}
		curContent = nil
	}

	for _, line := range lines {
		m := headingPat.FindStringSubmatch(line)
		if m != nil {
			flush()
			curHeading = m[2]
			continue
		}
		curContent = append(curContent, line)
	}
	flush()
	return chunks
}

var stopwords = map[string]bool{
	"the": true, "a": true, "an": true, "is": true, "are": true, "was": true,
	"were": true, "be": true, "been": true, "being": true, "have": true,
	"has": true, "had": true, "do": true, "does": true, "did": true,
	"will": true, "would": true, "could": true, "should": true, "may": true,
	"might": true, "shall": true, "can": true, "to": true, "of": true,
	"in": true, "for": true, "on": true, "with": true, "at": true,
	"by": true, "from": true, "as": true, "into": true, "through": true,
	"during": true, "before": true, "after": true, "above": true, "below": true,
	"between": true, "out": true, "off": true, "over": true, "under": true,
	"again": true, "further": true, "then": true, "once": true, "here": true,
	"there": true, "when": true, "where": true, "why": true, "how": true,
	"all": true, "each": true, "every": true, "both": true, "few": true,
	"more": true, "most": true, "other": true, "some": true, "such": true,
	"no": true, "nor": true, "not": true, "only": true, "own": true,
	"same": true, "so": true, "than": true, "too": true, "very": true,
	"just": true, "because": true, "but": true, "and": true, "or": true,
	"if": true, "while": true, "about": true, "up": true, "it": true,
	"its": true, "that": true, "this": true, "these": true, "those": true,
	"which": true, "who": true, "whom": true, "what": true, "i": true,
	"me": true, "my": true, "we": true, "our": true, "you": true,
	"your": true, "he": true, "him": true, "his": true, "she": true,
	"her": true, "they": true, "them": true, "their": true,
	"used": true, "using": true, "use": true, "set": true, "via": true,
}

var wordPat = regexp.MustCompile(`[a-zA-Z][a-zA-Z0-9]{1,}`)

func extractWords(text string) map[string]int {
	freq := make(map[string]int)
	for _, m := range wordPat.FindAllString(strings.ToLower(text), -1) {
		if !stopwords[m] && len(m) > 1 {
			freq[m]++
		}
	}
	return freq
}

func scoreChunk(chunk Chunk, focusWords map[string]int) int {
	text := chunk.Heading + " " + chunk.Content
	chunkWords := extractWords(text)

	score := 0
	for w := range focusWords {
		if _, ok := chunkWords[w]; ok {
			score += 3
		}
	}
	for w := range focusWords {
		headingLower := strings.ToLower(chunk.Heading)
		if strings.Contains(headingLower, w) {
			score += 5
		}
	}
	for _, phrase := range phrasesFromFocus(focusWords) {
		if strings.Contains(strings.ToLower(chunk.Heading), phrase) {
			score += 10
		}
		if strings.Contains(strings.ToLower(chunk.Content), phrase) {
			score += 5
		}
	}
	return score
}

func phrasesFromFocus(words map[string]int) []string {
	wordList := make([]string, 0, len(words))
	for w := range words {
		wordList = append(wordList, w)
	}
	if len(wordList) < 2 {
		return nil
	}
	var phrases []string
	for i := 0; i < len(wordList)-1; i++ {
		phrases = append(phrases, wordList[i]+" "+wordList[i+1])
	}
	return phrases
}

func pickChunk(chunks []scoredChunk, used map[int]bool, maxChars int) string {
	var best scoredChunk
	bestIdx := -1
	for i, sc := range chunks {
		if used[i] {
			continue
		}
		if sc.score > best.score {
			best = sc
			bestIdx = i
		}
	}
	if bestIdx == -1 || best.score <= 0 {
		return ""
	}
	used[bestIdx] = true
	excerpt := best.chunk.Heading + "\n" + best.chunk.Content
	if len(excerpt) > maxChars {
		excerpt = excerpt[:maxChars] + "…"
	}
	return excerpt
}

type scoredChunk struct {
	chunk Chunk
	score int
}

const excerptMaxChars = 1200

func ExtractSpecExcerpts(phases []judgePhasePayload, specText, reqText string) map[string]string {
	if len(phases) == 0 {
		return nil
	}

	specChunks := ChunkDocument(specText, "SPEC.md")
	reqChunks := ChunkDocument(reqText, "REQUIREMENTS.md")
	allChunks := append(specChunks, reqChunks...)

	result := make(map[string]string, len(phases))

	for _, phase := range phases {
		focus := phase.SpecFocus
		if focus == "" {
			focus = phase.Title
		}
		focusWords := extractWords(focus)

		var scored []scoredChunk
		for _, c := range allChunks {
			s := scoreChunk(c, focusWords)
			if s > 0 {
				scored = append(scored, scoredChunk{chunk: c, score: s})
			}
		}
		if len(scored) == 0 {
			continue
		}

		used := make(map[int]bool)
		var excerpts []string

		usedTotal := 0
		for {
			remaining := excerptMaxChars - usedTotal
			if remaining <= 20 {
				break
			}
			maxPerChunk := int(math.Min(float64(remaining), float64(excerptMaxChars)))
			excerpt := pickChunk(scored, used, maxPerChunk)
			if excerpt == "" {
				break
			}
			excerpts = append(excerpts, excerpt)
			usedTotal += len(excerpt) + 1
		}

		if len(excerpts) > 0 {
			result[phase.ID] = strings.Join(excerpts, "\n\n---\n\n")
		}
	}

	return result
}
