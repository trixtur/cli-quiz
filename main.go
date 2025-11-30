package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"sort"
	"strings"
	"sync"
	"syscall"
	"unsafe"
)

var allQuestions []question

type question struct {
	Domain  int               `json:"domain"`
	Prompt  string            `json:"question"`
	Options map[string]string `json:"options"`
	Answer  string            `json:"answer"`
}

type result struct {
	userAnswer rune
	correct    bool
}

var (
	activeRawState *syscall.Termios
	activeRawFD    int
	progressCount  int
	resultsGlobal  []result
	progressMu     sync.Mutex
)

func main() {
	setupSignalHandling()

	questions, err := loadQuestions()
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to load questions: %v\n", err)
		os.Exit(1)
	}

	reader := bufio.NewScanner(os.Stdin)
	allQuestions = questions

	fmt.Println("CSSLP Review Quiz (Domains 4-8)")
	fmt.Println("-------------------------------")
	fmt.Println("Answer each question with A, B, C, or D. Press Enter after each choice.")

	results := make([]result, len(questions))
	progressMu.Lock()
	resultsGlobal = results
	progressMu.Unlock()

	for i := 0; i < len(questions); {
		q := questions[i]
		userChoice, ok, jump := promptWithArrows(reader, q, i+1)
		if jump >= 0 {
			i = jump
			continue
		}
		if !ok {
			fmt.Println("\nInput ended unexpectedly. Exiting quiz.")
			return
		}
		progressMu.Lock()
		results[i] = result{
			userAnswer: userChoice,
			correct:    isCorrect(userChoice, q),
		}
		progressCount = i + 1
		progressMu.Unlock()
		// brief feedback before continuing
		showFeedback(q, results[i])
		fmt.Println("Press Enter to continue...")
		reader.Scan()
		fmt.Println()
		i++
	}

	score := 0
	for _, res := range results {
		if res.correct {
			score++
		}
	}

	printSummary(len(results), questions, results)
}

func isCorrect(choice rune, q question) bool {
	return strings.EqualFold(string(choice), q.Answer)
}

func loadQuestions() ([]question, error) {
	data, err := os.ReadFile("questions.json")
	if err != nil {
		return nil, err
	}
	var qs []question
	if err := json.Unmarshal(data, &qs); err != nil {
		return nil, err
	}
	return qs, nil
}

// promptWithArrows renders a selectable list with arrow key navigation.
// Returns selected answer, ok, and jumpIndex (>=0 when a search jump is requested).
func promptWithArrows(reader *bufio.Scanner, q question, number int) (rune, bool, int) {
	letters := sortedKeys(q.Options)
	if len(letters) == 0 {
		return 0, false, -1
	}

	choiceIdx := 0
	render := func() {
		width, rows := termSize()
		clearScreen()
		linesCount := len(letters) + 4 // title + blank + options + blank + instruction
		topPad := 0
		if rows > 0 {
			if pad := (rows - linesCount) / 2; pad > 0 {
				topPad = pad
			}
		}
		for i := 0; i < topPad; i++ {
			fmt.Println()
		}
		lines := []string{fmt.Sprintf("Q%d (Domain %d): %s", number, q.Domain, q.Prompt), ""}
		for i, letter := range letters {
			prefix := "  "
			if i == choiceIdx {
				prefix = "> "
			}
			line := fmt.Sprintf("%s%c) %s", prefix, letter, q.Options[string(letter)])
			lines = append(lines, line)
		}
		lines = append(lines, "", "Use ↑/↓ to select, Enter to confirm (A–D also works).")
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
	if res.correct {
		lines = append(lines, "Correct!")
	} else {
		lines = append(lines, "Incorrect.")
	}
	lines = append(lines,
		fmt.Sprintf("Your answer: %c", res.userAnswer),
		fmt.Sprintf("Correct answer: %s", q.Answer),
		"",
		fmt.Sprintf("Q (Domain %d): %s", q.Domain, q.Prompt),
	)
	for _, letter := range sortedKeys(q.Options) {
		lines = append(lines, fmt.Sprintf("  %c) %s", letter, q.Options[string(letter)]))
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
		if results[i].correct {
			score++
		}
	}

	fmt.Printf("You answered %d of %d correctly (%.1f%%).\n", score, answered, float64(score)*100/float64(answered))
	fmt.Println("\nReview:")
	for i := 0; i < answered; i++ {
		q := questions[i]
		user := results[i].userAnswer
		status := "incorrect"
		if results[i].correct {
			status = "correct"
		}
		fmt.Printf("Q%d: %s\n", i+1, status)
		fmt.Printf("  Your answer: %c\n", user)
		fmt.Printf("  Correct answer: %s\n\n", q.Answer)
	}
}

func setupSignalHandling() {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, os.Interrupt)
	go func() {
		<-ch
		if activeRawState != nil {
			restore(activeRawFD, activeRawState)
		}
		progressMu.Lock()
		answered := progressCount
		resCopy := append([]result(nil), resultsGlobal...)
		progressMu.Unlock()

		if answered == 0 {
			fmt.Println("\nNo answers recorded. Exiting.")
			os.Exit(1)
		}

		fmt.Println()
		printSummary(answered, allQuestions, resCopy)
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
