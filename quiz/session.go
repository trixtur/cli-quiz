package quiz

import (
	"encoding/json"
	"errors"
	"math/rand"
	"os"
	"strings"
	"sync"
	"time"
)

type Question struct {
	Domain  int               `json:"domain"`
	Prompt  string            `json:"question"`
	Options map[string]string `json:"options"`
	Answer  string            `json:"answer"`
}

type Result struct {
	UserAnswer string `json:"userAnswer"`
	Correct    bool   `json:"correct"`
}

type Session struct {
	Questions      []Question
	attempted      []bool
	completed      []bool
	results        []Result
	queue          []int
	completedCount int
	attemptedCount int
	mu             sync.Mutex
}

func LoadQuestions(path string) ([]Question, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var qs []Question
	if err := json.Unmarshal(data, &qs); err != nil {
		return nil, err
	}
	return qs, nil
}

func NewSession(qs []Question) *Session {
	rand.Seed(time.Now().UnixNano())
	queue := rand.Perm(len(qs))
	return &Session{
		Questions: qs,
		attempted: make([]bool, len(qs)),
		completed: make([]bool, len(qs)),
		results:   make([]Result, len(qs)),
		queue:     queue,
	}
}

func (s *Session) Current() (int, Question, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.queue) == 0 {
		return -1, Question{}, false
	}
	idx := s.queue[0]
	return idx, s.Questions[idx], true
}

func (s *Session) Answer(answer string) (Result, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.queue) == 0 {
		return Result{}, true, errors.New("quiz already completed")
	}
	idx := s.queue[0]
	s.queue = s.queue[1:]
	ansRune := normalize(answer)
	userAnswer := ""
	if ansRune != 0 {
		userAnswer = string(ansRune)
	}
	res := Result{
		UserAnswer: userAnswer,
		Correct:    strings.EqualFold(strings.TrimSpace(answer), s.Questions[idx].Answer),
	}
	if res.Correct && !s.completed[idx] {
		s.completed[idx] = true
		s.completedCount++
	}
	if !s.attempted[idx] {
		s.attempted[idx] = true
		s.results[idx] = res
		s.attemptedCount++
	}
	if !res.Correct {
		s.queue = append(s.queue, idx)
	}
	finished := len(s.queue) == 0
	return res, finished, nil
}

func (s *Session) BringToFront(target int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if target < 0 || target >= len(s.completed) || s.completed[target] {
		return
	}
	pos := -1
	for i, v := range s.queue {
		if v == target {
			pos = i
			break
		}
	}
	if pos == 0 {
		return
	}
	if pos == -1 {
		s.queue = append([]int{target}, s.queue...)
		return
	}
	s.queue = append([]int{target}, append(s.queue[:pos], s.queue[pos+1:]...)...)
}

func (s *Session) Progress() (completed, total int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.completedCount, len(s.Questions)
}

func (s *Session) AttemptedCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.attemptedCount
}

func (s *Session) Results() []Result {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Result, len(s.results))
	copy(out, s.results)
	return out
}

func (s *Session) Score() (score, answered int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, res := range s.results {
		if s.attempted[i] {
			answered++
			if res.Correct {
				score++
			}
		}
	}
	return score, answered
}

func (s *Session) Completed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.queue) == 0
}

func normalize(ans string) rune {
	ans = strings.TrimSpace(ans)
	if ans == "" {
		return 0
	}
	return rune(strings.ToUpper(ans)[0])
}
