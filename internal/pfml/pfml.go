package pfml

import "strings"

// StopStrings matches crates/core/src/pfml.rs STOP.
var StopStrings = []string{"<|plamo:tag|>", "<|plamo:bos|>"}

// Form specifies Japanese politeness level (plain = 常体, polite = 敬体).
type Form string

const (
	FormNone   Form = ""
	FormPlain  Form = "plain"
	FormPolite Form = "polite"
)

type Pair struct {
	Source      string
	Translation string
}

type TranslateRequest struct {
	Text       string
	SourceLang string
	TargetLang string
	Form       Form
	Context    string
	History    []Pair
	Glossary   []Pair
}

type attr struct {
	k, v string
}

func writeTag(b *strings.Builder, name string, attrs []attr, msg string) {
	b.WriteString("<|plamo:tag|>")
	b.WriteString(name)
	for _, a := range attrs {
		b.WriteString("<|plamo:key|>")
		b.WriteString(a.k)
		b.WriteString("<|plamo:val|>")
		b.WriteString(a.v)
	}
	b.WriteString("<|plamo:msg|>")
	b.WriteString(msg)
}

// Build constructs the PFML3 prompt byte-for-byte identically to crates/core/src/pfml.rs.
func Build(r TranslateRequest) string {
	var b strings.Builder
	writeTag(&b, "dataset", nil, "translation")
	if r.Context != "" {
		writeTag(&b, "context", nil, r.Context)
	}

	var src []attr
	if r.SourceLang != "" {
		src = []attr{{k: "lang", v: r.SourceLang}}
	}

	dst := []attr{{k: "lang", v: r.TargetLang}}
	if r.Form == FormPlain || r.Form == FormPolite {
		dst = append(dst, attr{k: "form", v: string(r.Form)})
	}

	for _, h := range r.History {
		writeTag(&b, "input", src, h.Source)
		writeTag(&b, "output", dst, h.Translation)
	}

	if len(r.Glossary) > 0 {
		var g strings.Builder
		for _, entry := range r.Glossary {
			g.WriteString("<|plamo:index|>")
			g.WriteString(entry.Source)
			g.WriteString("<|plamo:item|>")
			g.WriteString(entry.Translation)
		}
		writeTag(&b, "glossary", nil, g.String())
	}

	writeTag(&b, "input", src, r.Text)
	writeTag(&b, "output", dst, "")
	return b.String()
}

// TrimStop strips any trailing PLaMo special stop token if leaked into generated output.
func TrimStop(s string) string {
	for _, stop := range StopStrings {
		if idx := strings.Index(s, stop); idx >= 0 {
			s = s[:idx]
		}
	}
	return s
}
