package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"sort"
	"strings"
	"sync"
	"syscall"
	"unsafe"

	"quiz-cli/quiz"
	"quiz-cli/webapp"
)

var allQuestions []quiz.Question

var (
	activeRawState *syscall.Termios
	activeRawFD    int
	activeSession  *quiz.Session
	sessionMu      sync.Mutex
)

const (
	colorReset  = "\033[0m"
	colorGreen  = "\033[32m"
	colorRed    = "\033[31m"
	colorCyan   = "\033[36m"
	colorYellow = "\033[33m"
	colorBold   = "\033[1m"

	checkMark = "✅"
	crossMark = "❌"
)

type (
	question = quiz.Question
	result   = quiz.Result
)

func main() {
	mode := flag.String("mode", "cli", "cli or web")
	addr := flag.String("addr", ":8080", "listen address for web mode")
	flag.Parse()

	questions, err := quiz.LoadQuestions("questions.json")
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to load questions: %v\n", err)
		os.Exit(1)
	}

	allQuestions = questions

	if strings.EqualFold(*mode, "web") {
		if err := webapp.Run(*addr, questions); err != nil {
			fmt.Fprintf(os.Stderr, "web server error: %v\n", err)
			os.Exit(1)
		}
		return
	}

	runCLI(questions)
}

func runCLI(questions []quiz.Question) {
	session := quiz.NewSession(questions)
	sessionMu.Lock()
	activeSession = session
	sessionMu.Unlock()
	setupSignalHandling()

	reader := bufio.NewScanner(os.Stdin)

	fmt.Println(colorize("CSSLP Review Quiz (Domains 4-8)", colorBold+colorCyan))
	fmt.Println("-------------------------------")
	fmt.Println("Answer each question with A, B, C, or D. Press Enter after each choice.")

	for {
		idx, q, ok := session.Current()
		if !ok {
			break
		}
		completed, total := session.Progress()
		userChoice, inputOK, jump := promptWithArrows(reader, q, idx+1, completed, total)
		if jump >= 0 {
			session.BringToFront(jump)
			continue
		}
		if !inputOK {
			fmt.Println("\nInput ended unexpectedly. Exiting quiz.")
			return
		}

		res, finished, _ := session.Answer(string(userChoice))

		// brief feedback before continuing
		showFeedback(q, res)
		fmt.Println("Press Enter to continue...")
		reader.Scan()
		fmt.Println()
		if finished {
			break
		}
	}

	_, answered := session.Score()
	printSummary(answered, questions, session.Results())
}

// promptWithArrows renders a selectable list with arrow key navigation.
// Returns selected answer, ok, and jumpIndex (>=0 when a search jump is requested).
func promptWithArrows(reader *bufio.Scanner, q question, number int, completed, total int) (rune, bool, int) {
	letters := sortedKeys(q.Options)
	if len(letters) == 0 {
		return 0, false, -1
	}

	choiceIdx := 0
	render := func() {
		width, rows := termSize()
		clearScreen()
		progressLine := formatProgress(completed, total)
		header := colorize(fmt.Sprintf("Q%d (Domain %d): %s", number, q.Domain, q.Prompt), colorBold+colorCyan)
		lines := []string{progressLine, header, ""}
		for i, letter := range letters {
			prefix := "  "
			if i == choiceIdx {
				prefix = colorize("> ", colorYellow)
			}
			line := fmt.Sprintf("%s%c) %s", prefix, letter, q.Options[string(letter)])
			lines = append(lines, line)
		}
		lines = append(lines, "", colorize("Use ↑/↓ to select, Enter to confirm (A–D also works).", colorYellow))
		linesCount := len(lines)
		topPad := 0
		if rows > 0 {
			if pad := (rows - linesCount) / 2; pad > 0 {
				topPad = pad
			}
		}
		for i := 0; i < topPad; i++ {
			fmt.Println()
		}
		renderBlock(lines, width)
	}

	render()

	// switch to raw mode to capture arrow keys
	_, err := enableRaw(int(os.Stdin.Fd()))
	if err != nil {
		// fallback to typed input
		r, ok := fallbackPrompt(reader, letters)
		return r, ok, -1
	}
	defer func() {
		if activeRawState != nil {
			disableRaw(activeRawFD, activeRawState)
		}
	}()

	buf := make([]byte, 3)
	for {
		n, err := os.Stdin.Read(buf)
		if err != nil || n == 0 {
			return 0, false, -1
		}
		switch {
		case buf[0] == '\n' || buf[0] == '\r':
			return letters[choiceIdx], true, -1
		case buf[0] == 27 && n >= 3 && buf[1] == '[': // escape sequence
			switch buf[2] {
			case 'A': // up
				if choiceIdx > 0 {
					choiceIdx--
					render()
				}
			case 'B': // down
				if choiceIdx < len(letters)-1 {
					choiceIdx++
					render()
				}
			}
		case strings.ContainsRune("AaBbCcDd", rune(buf[0])):
			// allow direct letter entry
			ch := unicodeToLetter(rune(buf[0]))
			for i, l := range letters {
				if l == ch {
					choiceIdx = i
					render()
					return l, true, -1
				}
			}
		case buf[0] == '/':
			// temporarily leave raw mode for search
			if activeRawState != nil {
				disableRaw(activeRawFD, activeRawState)
			}
			target, ok := searchQuestions(reader)
			enableRaw(int(os.Stdin.Fd()))
			if target >= 0 && ok {
				return 0, true, target
			}
			render()
			continue
		}
	}
}

func fallbackPrompt(reader *bufio.Scanner, letters []rune) (rune, bool) {
	for {
		fmt.Print("Your answer (A-D): ")
		if !reader.Scan() {
			return 0, false
		}
		input := strings.TrimSpace(reader.Text())
		if len(input) == 0 {
			continue
		}
		ch := unicodeToLetter(rune(input[0]))
		for _, l := range letters {
			if ch == l {
				return ch, true
			}
		}
	}
}

func sortedKeys(opts map[string]string) []rune {
	keys := make([]string, 0, len(opts))
	for k := range opts {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	letters := make([]rune, 0, len(keys))
	for _, k := range keys {
		if len(k) > 0 {
			letters = append(letters, rune(strings.ToUpper(k)[0]))
		}
	}
	return letters
}

func formatProgress(completed, total int) string {
	if total <= 0 {
		return ""
	}
	if completed < 0 {
		completed = 0
	}
	if completed > total {
		completed = total
	}
	barWidth := 20
	filled := 0
	if total > 0 {
		filled = completed * barWidth / total
	}
	filledPart := colorize(strings.Repeat("#", filled), colorGreen+colorBold)
	emptyPart := strings.Repeat("-", barWidth-filled)
	bar := "[" + filledPart + emptyPart + "]"
	left := total - completed
	if left < 0 {
		left = 0
	}
	return fmt.Sprintf("%s %s%d/%d answered%s, %d left", bar, colorGreen, completed, total, colorReset, left)
}

// makeRaw sets the terminal into raw mode; returns previous state.
func makeRaw(fd int) (*syscall.Termios, error) {
	var oldState syscall.Termios
	if _, _, err := syscall.Syscall6(syscall.SYS_IOCTL, uintptr(fd), uintptr(syscall.TCGETS), uintptr(unsafe.Pointer(&oldState)), 0, 0, 0); err != 0 {
		return nil, err
	}
	newState := oldState
	newState.Lflag &^= syscall.ICANON | syscall.ECHO
	newState.Iflag &^= syscall.ICRNL
	if _, _, err := syscall.Syscall6(syscall.SYS_IOCTL, uintptr(fd), uintptr(syscall.TCSETS), uintptr(unsafe.Pointer(&newState)), 0, 0, 0); err != 0 {
		return nil, err
	}
	return &oldState, nil
}

func restore(fd int, state *syscall.Termios) {
	syscall.Syscall6(syscall.SYS_IOCTL, uintptr(fd), uintptr(syscall.TCSETS), uintptr(unsafe.Pointer(state)), 0, 0, 0)
}

func unicodeToLetter(ch rune) rune {
	ch = rune(strings.ToUpper(string(ch))[0])
	if ch >= 'A' && ch <= 'D' {
		return ch
	}
	return ch
}

func colorize(s, color string) string {
	if color == "" {
		return s
	}
	return color + s + colorReset
}

// searchQuestions returns (index, true) when found, or (-1, false) otherwise.
func searchQuestions(reader *bufio.Scanner) (int, bool) {
	clearScreen()
	fmt.Print("Search: ")
	if !reader.Scan() {
		return -1, false
	}
	term := strings.ToLower(strings.TrimSpace(reader.Text()))
	idx := -1
	for i, q := range allQuestions {
		if strings.Contains(strings.ToLower(q.Prompt), term) {
			idx = i
			break
		}
	}

	var lines []string
	if idx == -1 {
		lines = []string{"NOT FOUND", "", "Press Enter to return..."}
	} else {
		q := allQuestions[idx]
		lines = []string{
			fmt.Sprintf("Found at question %d (Domain %d)", idx+1, q.Domain),
			"",
			q.Prompt,
			"",
			"Press Enter to jump to this question...",
		}
	}
	width, rows := termSize()
	clearScreen()
	renderBlockWithVerticalCenter(lines, width, rows)
	reader.Scan()

	if idx == -1 {
		return -1, false
	}
	return idx, true
}

func showFeedback(q question, res result) {
	clearScreen()
	width, rows := termSize()
	lines := []string{
		"",
		"",
	}
	userLetter := '-'
	if res.UserAnswer != "" {
		userLetter = rune(res.UserAnswer[0])
	}
	if res.Correct {
		lines = append(lines, colorize(checkMark+" Correct!", colorGreen+colorBold))
	} else {
		lines = append(lines, colorize(crossMark+" Incorrect.", colorRed+colorBold))
	}
	lines = append(lines,
		colorize(fmt.Sprintf("Your answer: %c", userLetter), colorYellow),
		colorize(fmt.Sprintf("Correct answer: %s", q.Answer), colorGreen),
		"",
		colorize(fmt.Sprintf("Q (Domain %d): %s", q.Domain, q.Prompt), colorCyan+colorBold),
	)
	for _, letter := range sortedKeys(q.Options) {
		option := q.Options[string(letter)]
		line := fmt.Sprintf("  %c) %s", letter, option)
		if letter == unicodeToLetter(userLetter) {
			line = colorize(line, colorYellow)
		}
		lines = append(lines, line)
	}
	renderBlockWithVerticalCenter(lines, width, rows)
}

func printSummary(answered int, questions []question, results []result) {
	if answered > len(questions) {
		answered = len(questions)
	}
	if answered > len(results) {
		answered = len(results)
	}

	score := 0
	for i := 0; i < answered; i++ {
		if results[i].Correct {
			score++
		}
	}

	fmt.Println("\nReview:")

	rows := make([]string, answered)
	maxLen := 0
	for i := 0; i < answered; i++ {
		q := questions[i]
		user := "-"
		if results[i].UserAnswer != "" {
			user = results[i].UserAnswer
		}
		status := colorize(crossMark+" incorrect", colorRed+colorBold)
		if results[i].Correct {
			status = colorize(checkMark+" correct", colorGreen+colorBold)
		}
		line := fmt.Sprintf("Q%-3d %-9s Your:%s Correct:%s", i+1, status, user, q.Answer)
		rows[i] = line
		if l := len([]rune(line)); l > maxLen {
			maxLen = l
		}
	}

	width, _ := termSize()
	colWidth := maxLen + 2
	cols := 1
	if width > 0 && colWidth > 0 {
		if c := width / colWidth; c > 0 {
			cols = c
		}
	}
	if cols < 1 {
		cols = 1
	}
	rowsPerCol := (answered + cols - 1) / cols

	for r := 0; r < rowsPerCol; r++ {
		var parts []string
		for c := 0; c < cols; c++ {
			idx := c*rowsPerCol + r
			if idx >= answered {
				continue
			}
			parts = append(parts, padRight(rows[idx], colWidth))
		}
		fmt.Println(strings.TrimRight(strings.Join(parts, ""), " "))
	}
	fmt.Printf("You answered %d of %d correctly (%.1f%%).\n", score, answered, float64(score)*100/float64(answered))
}

func padRight(s string, width int) string {
	runes := []rune(s)
	if len(runes) >= width {
		return s
	}
	return s + strings.Repeat(" ", width-len(runes))
}

func setupSignalHandling() {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, os.Interrupt)
	go func() {
		<-ch
		if activeRawState != nil {
			restore(activeRawFD, activeRawState)
		}
		sessionMu.Lock()
		session := activeSession
		sessionMu.Unlock()

		if session == nil {
			fmt.Println("\nNo answers recorded. Exiting.")
			os.Exit(1)
		}

		_, answered := session.Score()
		if answered == 0 {
			fmt.Println("\nNo answers recorded. Exiting.")
			os.Exit(1)
		}

		fmt.Println()
		printSummary(answered, allQuestions, session.Results())
		os.Exit(0)
	}()
}

func enableRaw(fd int) (*syscall.Termios, error) {
	state, err := makeRaw(fd)
	if err == nil {
		activeRawState = state
		activeRawFD = fd
	}
	return state, err
}

func disableRaw(fd int, state *syscall.Termios) {
	restore(fd, state)
	if activeRawState == state {
		activeRawState = nil
	}
}

func centerLine(s string, width int) string {
	if width <= 0 {
		return s
	}
	runes := []rune(s)
	pad := (width - len(runes)) / 2
	if pad < 0 {
		pad = 0
	}
	return strings.Repeat(" ", pad) + s
}

func termSize() (int, int) {
	type winsize struct {
		Row    uint16
		Col    uint16
		Xpixel uint16
		Ypixel uint16
	}
	ws := &winsize{}
	_, _, err := syscall.Syscall6(syscall.SYS_IOCTL, uintptr(os.Stdout.Fd()), uintptr(syscall.TIOCGWINSZ), uintptr(unsafe.Pointer(ws)), 0, 0, 0)
	if err != 0 {
		return 0, 0
	}
	return int(ws.Col), int(ws.Row)
}

func clearScreen() {
	fmt.Print("\033[2J\033[H")
}

// renderBlock prints lines left-aligned within a centered block.
func renderBlock(lines []string, width int) {
	maxLen := 0
	for _, l := range lines {
		if len([]rune(l)) > maxLen {
			maxLen = len([]rune(l))
		}
	}
	margin := 0
	if width > 0 && maxLen < width {
		margin = (width - maxLen) / 2
	}
	space := strings.Repeat(" ", margin)
	for _, l := range lines {
		fmt.Println(space + l)
	}
}

func renderBlockWithVerticalCenter(lines []string, width, rows int) {
	if rows <= 0 {
		renderBlock(lines, width)
		return
	}
	topPad := (rows - len(lines)) / 2
	if topPad < 0 {
		topPad = 0
	}
	for i := 0; i < topPad; i++ {
		fmt.Println()
	}
	renderBlock(lines, width)
}
