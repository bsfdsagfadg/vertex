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

func SetAppInfo(ver, commit, bTime, goos, goarch string) {
	sendMsg(setAppInfoMsg{
		version:      ver,
		buildInfo:    fmt.Sprintf("Build: %s / %s", commit, bTime),
		platformInfo: fmt.Sprintf("Platform: %s/%s", goos, goarch),
	})
	sendMsg(logLineMsg(fmt.Sprintf("[vproxy] 启动成功: Version=%s, Commit=%s, Built=%s", ver, commit, bTime)))
	sendMsg(logLineMsg(fmt.Sprintf("[vproxy] 运行平台: %s/%s", goos, goarch)))
}

func SetInitialAdminPassword(pw string) {
	sendMsg(setAdminPasswordMsg(pw))
}

func IsTUIEnabled() bool {
	return enabled.Load()
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

func stringWidth(s string) int {
	return uniseg.StringWidth(s)
}

func padOrTrunc(s string, maxCol int) string {
	w := uniseg.StringWidth(s)
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
		gw := g.Width()
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
	w := uniseg.StringWidth(text)
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
			width: g.Width(),
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
