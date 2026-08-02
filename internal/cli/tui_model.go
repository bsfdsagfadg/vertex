package cli

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"charm.land/bubbletea/v2"
	"github.com/charmbracelet/lipgloss"
)

var ansiRegexp = regexp.MustCompile(`\033\[[0-9;]*[A-Za-z]`)

func stripANSI(s string) string {
	return ansiRegexp.ReplaceAllString(s, "")
}

var spinnerFrames = []rune(`⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏`)

type TuiModel struct {
	logBuffer    []string
	activeReqs   map[string]*ReqState
	appVersion   string
	buildInfo    string
	platformInfo string
	spinnerIdx   int
	width        int
	height       int
	scrollOffset int
}

type (
	logLineMsg         string
	startReqMsg        struct{ id string }
	finishReqMsg       struct{ id string }
	updateReqModelMsg  struct{ id, model string }
	updateReqStateMsg  struct{ id, state, color, detail string }
	updateReqWinnerMsg struct{ id, nodeName string }
	spinnerTickMsg     struct{}
	setAppInfoMsg      struct{ version, buildInfo, platformInfo string }
)

func (m TuiModel) Init() tea.Cmd {
	return tea.Batch(
		tea.Tick(120*time.Millisecond, func(t time.Time) tea.Msg {
			return spinnerTickMsg{}
		}),
	)
}

func (m TuiModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case spinnerTickMsg:
		m.spinnerIdx = (m.spinnerIdx + 1) % len(spinnerFrames)
		return m, tea.Tick(120*time.Millisecond, func(t time.Time) tea.Msg {
			return spinnerTickMsg{}
		})

	case logLineMsg:
		text := strings.TrimSpace(string(msg))
		if text == "" {
			return m, nil
		}
		for _, ln := range strings.Split(text, "\n") {
			ln = strings.TrimSpace(ln)
			if ln == "" {
				continue
			}
			m.logBuffer = append(m.logBuffer, ln)
		}
		if len(m.logBuffer) > maxLogs*3 {
			m.logBuffer = m.logBuffer[len(m.logBuffer)-maxLogs*3:]
		}
		return m, nil

	case startReqMsg:
		m.activeReqs[msg.id] = &ReqState{
			ID:        msg.id,
			Model:     "连接中...",
			State:     "🔗 连接中",
			Color:     "\033[90m",
			StartTime: time.Now(),
		}
		return m, nil

	case finishReqMsg:
		delete(m.activeReqs, msg.id)
		return m, nil

	case updateReqModelMsg:
		if req, ok := m.activeReqs[msg.id]; ok {
			req.Model = stripANSI(msg.model)
		}
		return m, nil

	case updateReqStateMsg:
		if req, ok := m.activeReqs[msg.id]; ok {
			req.State = stripANSI(msg.state)
			req.Color = msg.color
			if msg.detail != "" {
				req.Detail = stripANSI(msg.detail)
			}
		}
		return m, nil

	case updateReqWinnerMsg:
		if req, ok := m.activeReqs[msg.id]; ok {
			req.WinnerNode = msg.nodeName
		}
		return m, nil

	case tea.KeyPressMsg:
		switch {
		case msg.Mod == tea.ModCtrl && msg.Code == 'c':
			// Windows 不支持向当前进程发送 os.Interrupt；直接退出 TUI，
			// 由 main 监听 TUIDone 并执行与系统信号相同的优雅关闭流程。
			return m, tea.Quit
		case msg.Code == tea.KeyUp:
			if m.scrollOffset > 0 {
				m.scrollOffset--
			}
		case msg.Code == tea.KeyDown:
			m.scrollOffset++
		case msg.Code == tea.KeyPgUp:
			m.scrollOffset -= m.height / 2
			if m.scrollOffset < 0 {
				m.scrollOffset = 0
			}
		case msg.Code == tea.KeyPgDown:
			m.scrollOffset += m.height / 2
		case msg.Code == tea.KeyHome:
			m.scrollOffset = 0
		case msg.Code == tea.KeyEnd:
			m.scrollOffset = 1 << 30
		}
		return m, nil

	case setAppInfoMsg:
		m.appVersion = msg.version
		m.buildInfo = msg.buildInfo
		m.platformInfo = msg.platformInfo
		return m, nil

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil
	}

	return m, nil
}

var (
	cyanStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("6"))
	yellowStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))
	grayStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	redStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
)

func ansiToColor(code string) lipgloss.Color {
	switch code {
	case "\033[33m":
		return lipgloss.Color("3")
	case "\033[32m":
		return lipgloss.Color("2")
	case "\033[36m":
		return lipgloss.Color("6")
	case "\033[90m":
		return lipgloss.Color("8")
	default:
		return lipgloss.Color("7")
	}
}

func replaceFirstBorder(s string, repl rune) string {
	rs := []rune(s)
	for i, r := range rs {
		if r == '│' {
			rs[i] = repl
			return string(rs)
		}
	}
	return s
}

func replaceLastBorder(s string, repl rune) string {
	rs := []rune(s)
	for i := len(rs) - 1; i >= 0; i-- {
		if rs[i] == '│' {
			rs[i] = repl
			return string(rs)
		}
	}
	return s
}

func (m TuiModel) buildContent(bw int) string {
	biw := bw - 4
	if biw < minTermWidth-4 {
		biw = minTermWidth - 4
	}

	var sb strings.Builder

	bottomBorder := func() string {
		return "╰" + dashBar(bw-2) + "╯"
	}

	// ── Banner ──
	{
		prefix := "╭── 📢 Vertex AI Proxy "
		pw := stringWidth(prefix)
		d := bw - pw - 1
		if d < 0 {
			d = 0
		}
		sb.WriteString(yellowStyle.Render(prefix+dashBar(d)+"╮") + "\n")

		line1 := fmt.Sprintf("Version: %s | %s", m.appVersion, m.platformInfo)
		sb.WriteString(yellowStyle.Render("│") + " " + padOrTrunc(line1, biw) + " " + yellowStyle.Render("│") + "\n")
		sb.WriteString(yellowStyle.Render("│") + " " + padOrTrunc(m.buildInfo, biw) + " " + yellowStyle.Render("│") + "\n")

		warn := "⚠  本软件完全免费！付费即被骗，请退款。"
		sb.WriteString(yellowStyle.Render("│") + " " + redStyle.Render(padOrTrunc(warn, biw)) + " " + yellowStyle.Render("│") + "\n")
		sb.WriteString(yellowStyle.Render(bottomBorder()) + "\n")
	}

	// ── 最近系统日志 ──
	{
		prefix := "╭── 📝 最近系统日志 "
		pw := stringWidth(prefix)
		d := bw - pw - 1
		if d < 0 {
			d = 0
		}
		sb.WriteString(cyanStyle.Render(prefix+dashBar(d)+"╮") + "\n")

		var visualLines []string
		for i := len(m.logBuffer) - 1; i >= 0 && len(visualLines) < maxLogs*5; i-- {
			wrapped := wordWrap(m.logBuffer[i], biw)
			visualLines = append(wrapped, visualLines...)
		}
		if len(visualLines) > maxLogs {
			visualLines = visualLines[len(visualLines)-maxLogs:]
		}
		for i := 0; i < maxLogs; i++ {
			if i < len(visualLines) {
				sb.WriteString(cyanStyle.Render("│") + " " + visualLines[i] + " " + cyanStyle.Render("│") + "\n")
			} else {
				sb.WriteString(cyanStyle.Render("│") + " " + strings.Repeat(" ", biw) + " " + cyanStyle.Render("│") + "\n")
			}
		}
		sb.WriteString(cyanStyle.Render(bottomBorder()) + "\n")
	}

	// ── 请求追踪器 ──
	if len(m.activeReqs) > 0 {
		reqs := make([]*ReqState, 0, len(m.activeReqs))
		for _, r := range m.activeReqs {
			reqs = append(reqs, r)
		}
		sort.Slice(reqs, func(i, j int) bool {
			return reqs[i].StartTime.Before(reqs[j].StartTime)
		})

		prefix := "╭── 🚀 请求追踪器 "
		pw := stringWidth(prefix)
		d := bw - pw - 1
		if d < 0 {
			d = 0
		}
		sb.WriteString(cyanStyle.Render(prefix+dashBar(d)+"╮") + "\n")

		const separatorOverhead = 16
		totalColsWidth := bw - separatorOverhead

		idW := 8
		timeW := 6
		remaining := totalColsWidth - idW - timeW
		if remaining < 20 {
			remaining = 20
		}
		modelW := remaining * 25 / 100
		if modelW < 8 {
			modelW = 8
		}
		stateW := remaining * 20 / 100
		if stateW < 6 {
			stateW = 6
		}
		detailW := remaining - modelW - stateW
		if detailW < 6 {
			detailW = 6
		}

		// 表头
		sb.WriteString(fmt.Sprintf("%s %-*s %s %-*s %s %-*s %s %-*s %s %-*s %s\n",
			cyanStyle.Render("│"), idW, "ID", cyanStyle.Render("│"),
			modelW, "Model", cyanStyle.Render("│"),
			stateW, "State", cyanStyle.Render("│"),
			timeW, "Time", cyanStyle.Render("│"),
			detailW, "Details", cyanStyle.Render("│")))

		sep := fmt.Sprintf("%s%s%s%s%s%s%s%s%s%s%s\n",
			cyanStyle.Render("├"), dashBar(idW+2), cyanStyle.Render("┼"),
			dashBar(modelW+2), cyanStyle.Render("┼"),
			dashBar(stateW+2), cyanStyle.Render("┼"),
			dashBar(timeW+2), cyanStyle.Render("┼"),
			dashBar(detailW+2), cyanStyle.Render("┤"))
		sb.WriteString(sep)

		for _, r := range reqs {
			elapsed := time.Since(r.StartTime).Seconds()
			idVal := r.ID
			if len(idVal) > idW-2 {
				idVal = idVal[:idW-2]
			}

			detailStr := r.Detail
			if r.WinnerNode != "" {
				detailStr = "🏆 " + r.WinnerNode
				if r.Detail != "" {
					detailStr += " | " + r.Detail
				}
			}

			idCol := padOrTrunc(idVal, idW-2)
			modelCol := padOrTrunc(r.Model, modelW)
			stateCol := padOrTrunc(r.State, stateW)
			timeCol := fmt.Sprintf("%4.1fs", elapsed)
			timeCol = padOrTrunc(timeCol, timeW)
			detailCol := padOrTrunc(detailStr, detailW)

			spinnerCh := string(spinnerFrames[m.spinnerIdx])
			stateRendered := lipgloss.NewStyle().Foreground(ansiToColor(r.Color)).Render(stateCol)

			sb.WriteString(fmt.Sprintf("%s %s %s %s %s %s %s %s %s %s %s %s\n",
				cyanStyle.Render("│"),
				cyanStyle.Render(spinnerCh), idCol, cyanStyle.Render("│"),
				modelCol, cyanStyle.Render("│"),
				stateRendered, cyanStyle.Render("│"),
				timeCol, cyanStyle.Render("│"),
				grayStyle.Render(detailCol), cyanStyle.Render("│")))
		}
		sb.WriteString(cyanStyle.Render(bottomBorder()) + "\n")
	}

	return sb.String()
}

func (m TuiModel) View() tea.View {
	bw := m.width
	if bw < minTermWidth {
		bw = minTermWidth
	}

	content := m.buildContent(bw)

	if m.height <= 0 {
		v := tea.NewView(content)
		v.AltScreen = true
		return v
	}

	lines := strings.Split(content, "\n")
	totalLines := len(lines)

	if totalLines <= m.height {
		v := tea.NewView(content)
		v.AltScreen = true
		return v
	}

	maxStart := totalLines - m.height
	if m.scrollOffset > maxStart {
		m.scrollOffset = maxStart
	}
	start := m.scrollOffset

	visible := make([]string, m.height)
	copy(visible, lines[start:start+m.height])

	if start > 0 && len(visible[0]) > 0 {
		visible[0] = replaceFirstBorder(visible[0], '↑')
	}

	if start+m.height < totalLines {
		lastIdx := m.height - 1
		visible[lastIdx] = replaceLastBorder(visible[lastIdx], '↓')
	}

	// 当最后一行是底部 border 时嵌入位置文本
	if start+m.height >= totalLines {
		lastVis := visible[m.height-1]
		plain := stripANSI(lastVis)
		if strings.HasPrefix(plain, "╰") && strings.HasSuffix(plain, "╯") {
			pos := fmt.Sprintf(" %d/%d ", start+m.height, totalLines)
			innerRunes := []rune(plain)
			innerWidth := len(innerRunes) - 2
			posW := len([]rune(pos))
			if posW < innerWidth && posW > 0 {
				padL := (innerWidth - posW) / 2
				padR := innerWidth - posW - padL
				newInner := strings.Repeat("─", padL) + pos + strings.Repeat("─", padR)
				visible[m.height-1] = reconstructStyled(lastVis, "╰"+newInner+"╯")
			}
		}
	}

	v := tea.NewView(strings.Join(visible, "\n"))
	v.AltScreen = true
	return v
}

func reconstructStyled(original, plainText string) string {
	rs := []rune(original)
	var prefix, suffix string
	for i, r := range rs {
		if r == '\x1b' {
			j := i + 1
			for j < len(rs) && rs[j] != 'm' {
				j++
			}
			if j < len(rs) {
				prefix = string(rs[:j+1])
				break
			}
		}
	}
	// Plain text starts after ANSI prefix
	startPlain := len([]rune(prefix))
	// After plain text, there might be more ANSI
	for i := startPlain; i < len(rs); i++ {
		if rs[i] == '\x1b' {
			suffix = string(rs[i:])
			break
		}
	}
	return prefix + plainText + suffix
}
