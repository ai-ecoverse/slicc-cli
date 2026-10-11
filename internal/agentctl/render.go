package agentctl

import (
	"fmt"
	"io"
	"regexp"
	"strings"
	"sync"
	"unicode"
)

const summaryLimit = 200

type Style struct {
	TTY   bool
	Color bool
}

type RenderOptions struct {
	Out      io.Writer
	Err      io.Writer
	OutStyle Style
	ErrStyle Style
	Thinking bool
	Users    bool
	Runs     bool
}

type side struct {
	w     io.Writer
	style Style
	mid   bool
	wrote bool
}

func (s *side) write(text string) {
	if text == "" {
		return
	}
	_, _ = io.WriteString(s.w, text)
	s.wrote = true
	s.mid = !strings.HasSuffix(text, "\n")
}

func (s *side) lineStart() {
	if s.mid {
		s.write("\n")
	}
}

type Renderer struct {
	mu       sync.Mutex
	opts     RenderOptions
	out      side
	err      side
	tools    map[string]string
	lastText string
	thinking string
}

func NewRenderer(opts RenderOptions) *Renderer {
	return &Renderer{
		opts:  opts,
		out:   side{w: opts.Out, style: opts.OutStyle},
		err:   side{w: opts.Err, style: opts.ErrStyle},
		tools: map[string]string{},
	}
}

const (
	sgrBold   = "1"
	sgrDim    = "2"
	sgrItalic = "3"
	sgrRed    = "31"
	sgrYellow = "33"
	sgrCyan   = "36"
)

func paint(st Style, text string, codes ...string) string {
	if !st.Color || text == "" || len(codes) == 0 {
		return text
	}
	return "\x1b[" + strings.Join(codes, ";") + "m" + text + "\x1b[0m"
}

var escapes = regexp.MustCompile("\x1b\\[[0-?]*[ -/]*[@-~]|\x1b\\][^\x07\x1b]*(\x07|\x1b\\\\)?|\x1b[@-Z\\\\-_]")

func Clean(s string) string {
	s = escapes.ReplaceAllString(s, "")
	return strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' {
			return r
		}
		if unicode.IsControl(r) || r == '\u2028' || r == '\u2029' {
			return -1
		}
		return r
	}, s)
}

func OneLine(s string) string {
	s = strings.Join(strings.Fields(Clean(s)), " ")
	r := []rune(s)
	if len(r) <= summaryLimit {
		return s
	}
	return string(r[:summaryLimit]) + "…"
}

func (r *Renderer) toOut(text string) {
	if r.err.mid && r.sameScreen() {
		r.err.write("\n")
	}
	r.out.write(text)
}

func (r *Renderer) errLine(text string) {
	if r.out.mid && r.sameScreen() {
		r.out.write("\n")
	}
	r.err.lineStart()
	r.err.write(text + "\n")
}

func (r *Renderer) sameScreen() bool { return r.out.style.TTY && r.err.style.TTY }

func (r *Renderer) Render(e Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if e.T != EventThinking && r.thinking != "" {
		r.endThinking()
	}
	switch e.T {
	case EventText:
		r.text(e)
	case EventThinking:
		if r.opts.Thinking {
			r.think(e)
		}
	case EventUser:
		if r.opts.Users {
			r.user(e)
		}
	case EventTool:
		r.tool(e)
	case EventToolResult:
		r.toolResult(e)
	case EventNotice:
		r.notice(Clean(e.Text))
	case EventError:
		r.error(e.Message, e.Detail)
	case EventRun:
		if r.opts.Runs {
			r.run(e)
		}
	}
}

func (r *Renderer) text(e Event) {
	if e.ID != r.lastText && r.lastText != "" && r.out.wrote {
		r.out.lineStart()
		r.out.write("\n")
	}
	r.lastText = e.ID
	r.toOut(Clean(e.Delta))
}

func (r *Renderer) think(e Event) {
	st := r.err.style
	if r.thinking != e.ID {
		r.endThinking()
		if r.out.mid && r.sameScreen() {
			r.out.write("\n")
		}
		r.err.lineStart()
		if !st.TTY {
			r.err.write("thinking: ")
		}
		r.thinking = e.ID
	}
	r.err.write(paint(st, Clean(e.Delta), sgrDim, sgrItalic))
}

func (r *Renderer) endThinking() {
	r.thinking = ""
	r.err.lineStart()
}

func (r *Renderer) user(e Event) {
	r.out.lineStart()
	if r.out.wrote {
		r.out.write("\n")
	}
	r.out.write(paint(r.out.style, "> "+OneLineFull(e.Text), sgrBold) + "\n")
	r.lastText = ""
}

func OneLineFull(s string) string { return strings.Join(strings.Fields(Clean(s)), " ") }

func (r *Renderer) tool(e Event) {
	name := OneLine(e.Name)
	if e.Call != "" {
		r.tools[e.Call] = name
	}
	summary := OneLine(e.Summary)
	st := r.err.style
	if !st.TTY {
		r.errLine(strings.TrimSpace("tool " + name + ": " + summary))
		return
	}
	line := paint(st, "⚙ "+name, sgrBold, sgrCyan)
	if summary != "" {
		line += " " + paint(st, summary, sgrDim)
	}
	r.errLine(line)
}

func (r *Renderer) toolResult(e Event) {
	name := r.tools[e.Call]
	delete(r.tools, e.Call)
	ok := e.OK == nil || *e.OK
	summary := OneLine(e.Summary)
	st := r.err.style
	if !st.TTY {
		verdict := "ok"
		if !ok {
			verdict = "failed"
		}
		line := strings.TrimSpace("tool " + name + " " + verdict)
		if summary != "" {
			line += ": " + summary
		}
		r.errLine(line)
		return
	}
	if ok {
		r.errLine(paint(st, strings.TrimRight("  ↳ "+summary, " "), sgrDim))
		return
	}
	r.errLine(paint(st, strings.TrimRight("  ✗ "+summary, " "), sgrRed))
}

func (r *Renderer) notice(text string) {
	text = OneLineFull(text)
	st := r.err.style
	if !st.TTY {
		r.errLine("notice: " + text)
		return
	}
	r.errLine(paint(st, "• "+text, sgrYellow))
}

func (r *Renderer) error(message, detail string) {
	message, detail = OneLineFull(message), OneLine(detail)
	st := r.err.style
	if !st.TTY {
		line := "error: " + message
		if detail != "" {
			line += " (" + detail + ")"
		}
		r.errLine(line)
		return
	}
	line := paint(st, "✗ "+message, sgrRed)
	if detail != "" {
		line += " " + paint(st, detail, sgrDim)
	}
	r.errLine(line)
}

func (r *Renderer) run(e Event) {
	if e.State != "end" {
		return
	}
	r.out.lineStart()
	r.lastText = ""
	if e.Reason != "" && e.Reason != EndStop {
		r.notice("run ended: " + Clean(e.Reason))
	}
}

func (r *Renderer) Notice(format string, args ...any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.notice(fmt.Sprintf(format, args...))
}

func (r *Renderer) Error(format string, args ...any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.error(fmt.Sprintf(format, args...), "")
}

func (r *Renderer) Status(format string, args ...any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.errLine(paint(r.err.style, "slicc: "+fmt.Sprintf(format, args...), sgrDim))
}

func (r *Renderer) Finish() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.thinking != "" {
		r.endThinking()
	}
	r.out.lineStart()
	r.err.lineStart()
}
