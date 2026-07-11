package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	ahaclock "github.com/adewale/aha/internal/clock"
	ahaprogress "github.com/adewale/aha/internal/progress"
	"github.com/mattn/go-isatty"
)

type progressMode string

const (
	progressAuto  progressMode = "auto"
	progressOff   progressMode = "off"
	progressPlain progressMode = "plain"
	progressTTY   progressMode = "tty"
	progressJSON  progressMode = "json"
)

func selectProgressMode(requested string, finalJSON, tty bool) (progressMode, error) {
	mode := progressMode(strings.ToLower(strings.TrimSpace(requested)))
	if mode == "" {
		mode = progressAuto
	}
	switch mode {
	case progressAuto:
		if finalJSON && !tty {
			return progressOff, nil
		}
		if tty {
			return progressTTY, nil
		}
		return progressPlain, nil
	case progressOff, progressPlain, progressTTY, progressJSON:
		return mode, nil
	default:
		return progressOff, fmt.Errorf("invalid --progress %q: use auto, off, plain, tty, or json", requested)
	}
}

func writerIsTerminal(writer io.Writer) bool {
	file, ok := writer.(*os.File)
	if !ok {
		return false
	}
	fd := file.Fd()
	return isatty.IsTerminal(fd) || isatty.IsCygwinTerminal(fd)
}

type progressRenderer struct {
	mu       sync.Mutex
	writer   io.Writer
	mode     progressMode
	closed   bool
	ttyLine  bool
	lastTick map[ahaprogress.Phase]uint64
}

func newProgressRenderer(writer io.Writer, mode progressMode) *progressRenderer {
	return &progressRenderer{writer: writer, mode: mode, lastTick: map[ahaprogress.Phase]uint64{}}
}

func (r *progressRenderer) Observe(event ahaprogress.Event) {
	if r == nil || r.mode == progressOff || r.writer == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return
	}
	switch r.mode {
	case progressPlain:
		if event.Kind == ahaprogress.Advanced {
			return
		}
		_, _ = fmt.Fprintln(r.writer, formatProgressLine(event))
	case progressTTY:
		if event.Kind == ahaprogress.Advanced && !r.shouldRenderAdvance(event) {
			return
		}
		_, _ = fmt.Fprintf(r.writer, "\r\033[K%s", formatProgressLine(event))
		r.ttyLine = true
		if event.Kind == ahaprogress.Completed || event.Kind == ahaprogress.Cancelled || event.Kind == ahaprogress.Failed {
			_, _ = fmt.Fprintln(r.writer)
			r.ttyLine = false
		}
	case progressJSON:
		payload := struct {
			Kind      ahaprogress.Kind  `json:"kind"`
			Phase     ahaprogress.Phase `json:"phase"`
			Current   uint64            `json:"current,omitempty"`
			Total     ahaprogress.Total `json:"total"`
			Unit      ahaprogress.Unit  `json:"unit,omitempty"`
			ElapsedMS int64             `json:"elapsed_ms"`
		}{Kind: event.Kind, Phase: event.Phase, Current: event.Current, Total: event.Total, Unit: event.Unit, ElapsedMS: event.Elapsed.Milliseconds()}
		if encoded, err := json.Marshal(payload); err == nil {
			_, _ = r.writer.Write(append(encoded, '\n'))
		}
	}
}

func (r *progressRenderer) shouldRenderAdvance(event ahaprogress.Event) bool {
	var tick uint64
	if event.Total.Known && event.Total.Value > 0 {
		current := event.Current
		if current > event.Total.Value {
			current = event.Total.Value
		}
		tick = current * 100 / event.Total.Value
	} else {
		tick = uint64(event.Elapsed / time.Second)
	}
	previous, seen := r.lastTick[event.Phase]
	if seen && previous == tick {
		return false
	}
	r.lastTick[event.Phase] = tick
	return true
}

func formatProgressLine(event ahaprogress.Event) string {
	parts := []string{"progress", "phase=" + string(event.Phase), "state=" + string(event.Kind)}
	if event.Current > 0 || event.Total.Known {
		parts = append(parts, fmt.Sprintf("current=%d", event.Current))
	}
	if event.Total.Known {
		parts = append(parts, fmt.Sprintf("total=%d", event.Total.Value))
		if event.Total.Value > 0 {
			current := event.Current
			if current > event.Total.Value {
				current = event.Total.Value
			}
			parts = append(parts, fmt.Sprintf("percent=%d%%", current*100/event.Total.Value))
		}
	}
	if event.Unit != ahaprogress.UnitNone {
		parts = append(parts, "unit="+string(event.Unit))
	}
	parts = append(parts, "elapsed="+formatElapsed(event.Elapsed))
	return strings.Join(parts, " ")
}

func formatElapsed(elapsed time.Duration) string {
	if elapsed < time.Second {
		return fmt.Sprintf("%dms", elapsed.Milliseconds())
	}
	return elapsed.Round(100 * time.Millisecond).String()
}

func (r *progressRenderer) Close() error {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return nil
	}
	if r.mode == progressTTY && r.ttyLine && r.writer != nil {
		_, _ = fmt.Fprintln(r.writer)
	}
	r.closed = true
	return nil
}

type progressSession struct {
	Tracker  *ahaprogress.Tracker
	renderer *progressRenderer
}

func newProgressSession(stderr io.Writer, requested string, finalJSON bool) (*progressSession, error) {
	mode, err := selectProgressMode(requested, finalJSON, writerIsTerminal(stderr))
	if err != nil {
		return nil, err
	}
	renderer := newProgressRenderer(stderr, mode)
	if mode == progressOff {
		return &progressSession{renderer: renderer}, nil
	}
	return &progressSession{Tracker: ahaprogress.NewTracker(renderer, ahaclock.RealClock{}), renderer: renderer}, nil
}

func (s *progressSession) Close() error {
	if s == nil {
		return nil
	}
	return s.renderer.Close()
}
