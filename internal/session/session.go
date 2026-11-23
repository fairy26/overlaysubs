package session

import (
	"context"
	"strings"
	"sync"
	"unicode"

	"github.com/fairy26/overlaysubs/internal/pfml"
)

const (
	MaxHistory       = 3
	MinSentenceChars = 10
)

type Stage string

const (
	StageStreaming Stage = "streaming"
	StageFinal     Stage = "final"
)

type Caption struct {
	ID          uint64 `json:"id"`
	Stage       Stage  `json:"stage"`
	Lang        string `json:"lang"`
	Source      string `json:"source"`
	Translation string `json:"translation"`
}

type Translator interface {
	Translate(ctx context.Context, req pfml.TranslateRequest, onPiece func(piece string)) (string, error)
}

type Config struct {
	SourceLang string
	TargetLang string
	Form       pfml.Form
	Topic      string
	Glossary   []pfml.Pair
	UseContext bool
}

var hallucinations = []string{
	"thank you for watching",
	"thanks for watching",
	"please subscribe",
	"subtitles by",
	"ご視聴ありがとうございました",
	"チャンネル登録",
	"字幕",
	"시청해 주셔서 감사합니다",
}

// IsHallucination filters Whisper's well-known silence/music hallucinations (from crates/core/src/session.rs).
func IsHallucination(text string) bool {
	trimmed := strings.TrimSpace(text)
	trimmed = strings.TrimFunc(trimmed, func(r rune) bool {
		return r == '。' || r == '♪' || (r < 128 && unicode.IsPunct(r))
	})
	lower := strings.ToLower(trimmed)
	alnum := 0
	for _, r := range lower {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			alnum++
		}
	}
	if alnum < 2 {
		return true
	}
	for _, h := range hallucinations {
		if strings.Contains(lower, h) {
			return true
		}
	}
	return false
}

func HasWords(text string) bool {
	for _, r := range text {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return true
		}
	}
	return false
}

func isCloser(r rune) bool {
	switch r {
	case '"', '\'', '」', '』', ')', '）', '”', '’':
		return true
	}
	return false
}

func isSentenceEnd(r rune) bool {
	switch r {
	case '.', '?', '!', '。', '？', '！', '…':
		return true
	}
	return false
}

func EndsSentence(text string) bool {
	t := strings.TrimRightFunc(text, unicode.IsSpace)
	t = strings.TrimRightFunc(t, isCloser)
	runes := []rune(t)
	return len(runes) > 0 && isSentenceEnd(runes[len(runes)-1])
}

func Punctuate(text, lang string) string {
	if EndsSentence(text) {
		return text
	}
	if lang == "ja" || lang == "zh" {
		return text + "。"
	}
	return text + "."
}

// SplitSentences splits a long utterance into sentences of at least MinSentenceChars runes,
// matching crates/core/src/session.rs.
func SplitSentences(text string) []string {
	runes := []rune(text)
	var out []string
	var cur []rune

	for i, c := range runes {
		cur = append(cur, c)
		var next rune
		hasNext := i+1 < len(runes)
		if hasNext {
			next = runes[i+1]
		}
		boundary := false
		switch {
		case c == '。' || c == '？' || c == '！':
			boundary = !hasNext || (!isCloser(next) && next != '。' && next != '？' && next != '！')
		case c == '.' || c == '?' || c == '!' || c == '…':
			boundary = hasNext && unicode.IsSpace(next)
		case isCloser(c):
			trimmed := strings.TrimRightFunc(string(cur), isCloser)
			tr := []rune(trimmed)
			boundary = len(tr) > 0 && isSentenceEnd(tr[len(tr)-1]) && hasNext && unicode.IsSpace(next)
		}
		if boundary {
			s := strings.TrimSpace(string(cur))
			if len([]rune(s)) >= MinSentenceChars {
				out = append(out, s)
				cur = cur[:0]
			}
		}
	}

	tail := strings.TrimSpace(string(cur))
	if tail != "" {
		if len(out) > 0 && len([]rune(tail)) < MinSentenceChars {
			last := out[len(out)-1]
			lr := []rune(last)
			sep := " "
			if len(lr) > 0 && (lr[len(lr)-1] == '。' || lr[len(lr)-1] == '？' || lr[len(lr)-1] == '！') {
				sep = ""
			}
			out[len(out)-1] = last + sep + tail
		} else {
			out = append(out, tail)
		}
	}
	return out
}

type Session struct {
	mu      sync.Mutex
	cfg     Config
	history []pfml.Pair
	nextID  uint64
}

func New(cfg Config) *Session {
	return &Session{cfg: cfg}
}

func (s *Session) UpdateConfig(cfg Config) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cfg = cfg
}

func (s *Session) History() []pfml.Pair {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := make([]pfml.Pair, len(s.history))
	copy(cp, s.history)
	return cp
}

// HandleUtterance filters hallucinations, splits into sentences, translates with previous utterances
// as context, streams pieces via emit, and updates the rolling history.
func (s *Session) HandleUtterance(ctx context.Context, text string, mt Translator, emit func(Caption)) error {
	if IsHallucination(text) || !HasWords(text) {
		return nil
	}

	s.mu.Lock()
	cfg := s.cfg
	s.mu.Unlock()

	srcLang := cfg.SourceLang
	if srcLang == "" {
		srcLang = "en"
	}
	targetLang := cfg.TargetLang
	if targetLang == "" {
		targetLang = "ja"
	}

	sentences := SplitSentences(Punctuate(strings.TrimSpace(text), srcLang))
	for _, sentence := range sentences {
		s.mu.Lock()
		cfg = s.cfg
		id := s.nextID
		s.nextID++
		var hist []pfml.Pair
		if cfg.UseContext && len(s.history) > 0 {
			hist = make([]pfml.Pair, len(s.history))
			copy(hist, s.history)
		}
		s.mu.Unlock()

		var matchingGlossary []pfml.Pair
		for _, g := range cfg.Glossary {
			if g.Source != "" && strings.Contains(sentence, g.Source) {
				matchingGlossary = append(matchingGlossary, g)
			}
		}

		var form pfml.Form
		if targetLang == "ja" {
			form = cfg.Form
		}

		req := pfml.TranslateRequest{
			Text:       sentence,
			SourceLang: srcLang,
			TargetLang: targetLang,
			Form:       form,
			Context:    cfg.Topic,
			History:    hist,
			Glossary:   matchingGlossary,
		}

		var acc strings.Builder
		translated, err := mt.Translate(ctx, req, func(piece string) {
			if piece == "" {
				return
			}
			acc.WriteString(piece)
			if emit != nil {
				emit(Caption{
					ID:          id,
					Stage:       StageStreaming,
					Lang:        srcLang,
					Source:      sentence,
					Translation: strings.TrimSpace(acc.String()),
				})
			}
		})
		if err != nil {
			return err
		}
		translated = strings.TrimSpace(translated)
		if !HasWords(translated) {
			continue
		}

		if emit != nil {
			emit(Caption{
				ID:          id,
				Stage:       StageFinal,
				Lang:        srcLang,
				Source:      sentence,
				Translation: translated,
			})
		}

		s.mu.Lock()
		s.history = append(s.history, pfml.Pair{Source: sentence, Translation: translated})
		if len(s.history) > MaxHistory {
			s.history = s.history[len(s.history)-MaxHistory:]
		}
		s.mu.Unlock()
	}
	return nil
}
