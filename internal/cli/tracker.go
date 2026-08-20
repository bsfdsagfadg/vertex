package cli

import (
	"fmt"
	"io"
	"log"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"charm.land/bubbletea/v2"
	"github.com/bsfdsagfadg/vertex/internal/buildinfo"
	"github.com/charmbracelet/x/ansi"
	"github.com/rivo/uniseg"
)

type ReqState struct {
	ID         string
	Model      string
	State      string
	Color      string
	WinnerNode string
	Detail     string
	StartTime  time.Time
}

const (
	minTermWidth = 60
	maxLogs      = 10
)

var (
	//nolint:gochecknoglobals
	program atomic.Pointer[tea.Program]
	//nolint:gochecknoglobals
	programDone chan struct{}
	//nolint:gochecknoglobals
	enabled atomic.Bool
	//nolint:gochecknoglobals
	additionalLogWriter io.Writer
)

func sendMsg(msg tea.Msg) {
	if !enabled.Load() {
		return
	}
	p := program.Load()
	if p == nil {
		return
	}
	p.Send(msg)
}

func SetAppInfo(info buildinfo.BuildInfo, goos, goarch string) {
	sendMsg(setAppInfoMsg{
		version:      info.Version,
		buildInfo:    formatBuildLine(info),
		platformInfo: fmt.Sprintf("Platform: %s/%s", goos, goarch),
	})
	sendMsg(logLineMsg(fmt.Sprintf("[vproxy] build_info version=%s commit=%s build_time=%s dirty=%t source=%s",
		info.Version, info.Commit, info.BuildTime, info.Dirty, info.Source)))
	sendMsg(logLineMsg(fmt.Sprintf("[vproxy] 运行平台: %s/%s", goos, goarch)))
}

func formatBuildLine(info buildinfo.BuildInfo) string {
	parts := []string{"Build: " + info.Source}
	if info.Commit != "" {
		parts = append(parts, info.Commit)
	}
	if info.BuildTime != "" {
		parts = append(parts, info.BuildTime)
	}
	if info.Dirty {
		parts = append(parts, "dirty")
	}
	return strings.Join(parts, " / ")
}

func InitTracker(fileLogger io.Writer) {
	additionalLogWriter = fileLogger
	programDone = make(chan struct{})

	fileInfo, err := os.Stdout.Stat()
	if err == nil && (fileInfo.Mode()&os.ModeCharDevice) != 0 {
		enabled.Store(true)
		p := tea.NewProgram(
			TuiModel{
				width:      80,
				height:     24,
				activeReqs: make(map[string]*ReqState),
			},
			tea.WithoutSignalHandler(),
		)
		program.Store(p)

		go func() {
			defer func() {
				if r := recover(); r != nil {
					log.Printf("[TUI] Bubble Tea 运行崩溃: %v", r)
				}
			}()
			_, _ = p.Run()
			close(programDone)
		}()

		log.SetOutput(logInterceptor{})
	} else {
		if fileLogger != nil {
			log.SetOutput(io.MultiWriter(os.Stderr, fileLogger))
		}
	}
}

func StopTUI() {
	if !enabled.Load() {
		return
	}
	p := program.Load()
	if p == nil {
		return
	}
	p.Quit()
	<-programDone
	enabled.Store(false)
}

func TUIDone() <-chan struct{} {
	return programDone
}

func StartReq(id string) {
	sendMsg(startReqMsg{id: id})
}

func UpdateReqModel(id, model string) {
	sendMsg(updateReqModelMsg{id: id, model: model})
}

func UpdateReqState(id, state, color, detail string) {
	sendMsg(updateReqStateMsg{id: id, state: state, color: color, detail: detail})
}

func UpdateReqWinner(id, nodeName string) {
	sendMsg(updateReqWinnerMsg{id: id, nodeName: nodeName})
}

func FinishReq(id string) {
	sendMsg(finishReqMsg{id: id})
}

type logInterceptor struct{}

func (logInterceptor) Write(p []byte) (int, error) {
	if additionalLogWriter != nil {
		_, _ = additionalLogWriter.Write(p)
	}
	text := strings.TrimSpace(string(p))
	if text != "" {
		sendMsg(logLineMsg(stripANSI(text)))
	}
	return len(p), nil
}

// stringWidth 使用 Bubble Tea 渲染引擎同源的 x/ansi 宽度计算（EastAsianWidth=false），
// 与 TUI 实际渲染布局保持一致，避免 box-drawing/emoji 宽度歧义导致边框错位。
func stringWidth(s string) int {
	return ansi.StringWidth(s)
}

func padOrTrunc(s string, maxCol int) string {
	w := ansi.StringWidth(s)
	if w <= maxCol {
		return s + strings.Repeat(" ", maxCol-w)
	}
	if maxCol <= 2 {
		return ".."
	}
	var sb strings.Builder
	cur := 0
	g := uniseg.NewGraphemes(s)
	for g.Next() {
		gw := ansi.StringWidth(g.Str())
		if cur+gw > maxCol-2 {
			break
		}
		sb.WriteString(g.Str())
		cur += gw
	}
	sb.WriteString("..")
	cur += 2
	if cur < maxCol {
		sb.WriteString(strings.Repeat(" ", maxCol-cur))
	}
	return sb.String()
}

func wordWrap(text string, maxCol int) []string {
	if maxCol <= 0 {
		return []string{text}
	}
	w := ansi.StringWidth(text)
	if w <= maxCol {
		return []string{text + strings.Repeat(" ", maxCol-w)}
	}

	g := uniseg.NewGraphemes(text)
	type graphemeItem struct {
		str   string
		width int
	}
	var items []graphemeItem
	for g.Next() {
		items = append(items, graphemeItem{
			str:   g.Str(),
			width: ansi.StringWidth(g.Str()),
		})
	}

	var lines []string
	var cur strings.Builder
	curW := 0

	i := 0
	for i < len(items) {
		item := items[i]
		gw := item.width

		if item.str == " " && curW > 0 {
			nextW := 0
			j := i + 1
			for j < len(items) && items[j].str != " " {
				nextW += items[j].width
				j++
			}
			if curW+1+nextW > maxCol {
				lines = append(lines, cur.String()+strings.Repeat(" ", maxCol-curW))
				cur.Reset()
				curW = 0
				i++
				continue
			}
		}

		if curW+gw > maxCol {
			lines = append(lines, cur.String()+strings.Repeat(" ", maxCol-curW))
			cur.Reset()
			curW = 0
		}

		cur.WriteString(item.str)
		curW += gw
		i++
	}

	if cur.Len() > 0 {
		lines = append(lines, cur.String()+strings.Repeat(" ", maxCol-curW))
	}
	if len(lines) == 0 {
		lines = []string{strings.Repeat(" ", maxCol)}
	}
	return lines
}

func dashBar(w int) string {
	if w <= 0 {
		return ""
	}
	return strings.Repeat("─", w)
}
