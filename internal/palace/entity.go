package palace

import (
	_ "embed"
	"encoding/json"
	"regexp"
	"sort"
	"strings"
	"sync"
)

// Entity extraction for mining, ported from the frozen miner
// (_extract_entities_for_metadata) + entity_detector. The goal is the same: pull
// the proper-noun-ish tokens out of a chunk's content so co-occurrence within a
// wing can later materialise hallways, while filtering out ordinary capitalized
// words (sentence starters, common content words). Two vendored data files drive
// the filtering — a COCA common-word list and a known-systems compound list —
// embedded so the binary is self-contained.

//go:embed data/coca_content_words.json
var cocaJSON []byte

//go:embed data/known_systems.json
var knownSystemsJSON []byte

const (
	// entityExtractWindow bounds how much of a chunk is scanned for entities, so a
	// huge drawer cannot make extraction quadratic (frozen _ENTITY_EXTRACT_WINDOW).
	entityExtractWindow = 5000
	// entityMetadataLimit caps how many entities ride a drawer's metadata, sorted
	// so the cap drops whole names, never a partial (frozen _ENTITY_METADATA_LIMIT).
	entityMetadataLimit = 25
	// entityMinFreq / entityMinLen are the inclusion thresholds: a candidate must
	// appear at least twice and be longer than two characters, matching frozen.
	entityMinFreq = 2
	entityMinLen  = 2 // strictly-greater test below, so effective minimum length is 3
)

// entityStoplist is the frozen _ENTITY_STOPLIST: capitalized words that start
// sentences or name days/months/roles and are never entities. Membership is
// case-sensitive (candidates are matched as-found, capitalized).
var entityStoplist = map[string]struct{}{}

// candidateWordRE matches an entity candidate: an uppercase letter followed by
// letters. \p{Lu}/\p{L} keep it Unicode-aware (ASCII, accented Latin, Cyrillic),
// the faithful-enough stand-in for the frozen per-locale candidate patterns; the
// full i18n pattern set is a later refinement. Digits/hyphens are not part of a
// single-word candidate — multi-token names like "Claude Code" or "GPT-4o" are
// caught by the known-systems prepass instead.
var candidateWordRE = regexp.MustCompile(`\p{Lu}[\p{L}]*`)

// entityData is the lazily-loaded, parsed form of the two vendored files: the COCA
// words as a lowercased set, and the known-systems compounds as boundary-anchored
// case-insensitive matchers ordered longest-first so the longest compound wins.
type entityData struct {
	coca         map[string]struct{}
	knownSystems []knownSystem
}

// knownSystem pairs a compound's canonical name with its compiled matcher.
type knownSystem struct {
	name string
	re   *regexp.Regexp
}

var (
	entityOnce sync.Once
	entityDB   entityData
)

// loadEntityData parses the embedded JSON once. Like the frozen loader it degrades
// gracefully: a malformed or missing file yields an empty set rather than a panic,
// so extraction still runs (just without that filter/list).
func loadEntityData() entityData {
	entityOnce.Do(func() {
		var coca struct {
			Words []string `json:"words"`
		}
		_ = json.Unmarshal(cocaJSON, &coca)
		set := make(map[string]struct{}, len(coca.Words))
		for _, w := range coca.Words {
			set[strings.ToLower(w)] = struct{}{}
		}

		var known struct {
			Compounds []string `json:"compounds"`
		}
		_ = json.Unmarshal(knownSystemsJSON, &known)
		// Longest-first so "Claude Sonnet 4.5" masks before "Claude", matching the
		// frozen sort(key=len, reverse=True) longest-match-wins behaviour.
		valid := make([]string, 0, len(known.Compounds))
		for _, c := range known.Compounds {
			if strings.TrimSpace(c) != "" {
				valid = append(valid, c)
			}
		}
		sort.SliceStable(valid, func(i, j int) bool { return len(valid[i]) > len(valid[j]) })
		systems := make([]knownSystem, 0, len(valid))
		for _, c := range valid {
			// (?i) case-insensitive, \b word boundaries (ASCII — sufficient for these
			// English compounds). RE2 has no lookbehind, so \b stands in for the
			// frozen (?<!\w)…(?!\w); all compounds begin and end with a word char.
			re, err := regexp.Compile(`(?i)\b` + regexp.QuoteMeta(c) + `\b`)
			if err != nil {
				continue
			}
			systems = append(systems, knownSystem{name: c, re: re})
		}

		entityDB = entityData{coca: set, knownSystems: systems}
	})
	return entityDB
}

// extractEntities returns the entities stamped on a mined drawer's metadata, in
// sorted order and capped at entityMetadataLimit. It follows the frozen pipeline:
// scan a bounded window, mask known-system compounds (counting each), then count
// capitalized candidates that survive the stoplist and COCA filters, and keep
// every token seen at least twice and longer than two characters.
func extractEntities(content string) []string {
	db := loadEntityData()

	// Bound the scan by runes so multibyte content is windowed at a character
	// count, not a byte count (mirrors Python slicing the str).
	runes := []rune(content)
	if len(runes) > entityExtractWindow {
		runes = runes[:entityExtractWindow]
	}
	window := string(runes)

	// Known-systems prepass: mask each compound's matches to spaces (preserving
	// length so later indices stay valid) and remember how often each occurred, so
	// a compound counts as one entity and its constituent words are not recounted.
	freq := map[string]int{}
	for _, ks := range db.knownSystems {
		locs := ks.re.FindAllStringIndex(window, -1)
		if len(locs) == 0 {
			continue
		}
		freq[ks.name] += len(locs)
		b := []byte(window)
		for _, loc := range locs {
			for i := loc[0]; i < loc[1]; i++ {
				b[i] = ' '
			}
		}
		window = string(b)
	}

	// Capitalized single-word candidates from the masked window.
	for _, w := range candidateWordRE.FindAllString(window, -1) {
		if _, stop := entityStoplist[w]; stop {
			continue
		}
		if _, common := db.coca[strings.ToLower(w)]; common {
			continue
		}
		freq[w]++
	}

	// Keep tokens seen at least twice and longer than two characters.
	matched := make([]string, 0, len(freq))
	for w, c := range freq {
		if c >= entityMinFreq && len([]rune(w)) > entityMinLen {
			matched = append(matched, w)
		}
	}
	sort.Strings(matched)
	if len(matched) > entityMetadataLimit {
		matched = matched[:entityMetadataLimit]
	}
	return matched
}

func init() {
	for _, w := range []string{
		"The", "This", "That", "These", "Those", "When", "Where", "What", "Why",
		"Who", "Which", "How", "After", "Before", "Then", "Now", "Here", "There",
		"And", "But", "Or", "Yet", "So", "If", "Else", "Yes", "No", "Maybe", "Okay",
		"User", "Assistant", "System", "Tool",
		"Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday", "Sunday",
		"January", "February", "March", "April", "May", "June", "July", "August",
		"September", "October", "November", "December",
	} {
		entityStoplist[w] = struct{}{}
	}
}
