package webapp

import (
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"quiz-cli/quiz"
)

type Server struct {
	session   *quiz.Session
	questions []quiz.Question
	mu        sync.Mutex
}

func Run(addr string, questions []quiz.Question) error {
	s := &Server{
		session:   quiz.NewSession(questions),
		questions: questions,
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleHome)
	mux.HandleFunc("/api/state", s.handleState)
	mux.HandleFunc("/api/answer", s.handleAnswer)
	mux.HandleFunc("/api/summary", s.handleSummary)
	mux.HandleFunc("/api/reset", s.handleReset)
	mux.HandleFunc("/api/jump", s.handleJump)
	server := &http.Server{
		Addr:         addr,
		Handler:      mux,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 10 * time.Second,
	}
	fmt.Printf("Web quiz available at http://%s\n", addr)
	return server.ListenAndServe()
}

type stateResponse struct {
	Finished bool             `json:"finished"`
	Question *questionPayload `json:"question,omitempty"`
	Progress progressPayload  `json:"progress"`
	Summary  *summaryPayload  `json:"summary,omitempty"`
}

type questionPayload struct {
	Index   int               `json:"index"`
	Domain  int               `json:"domain"`
	Prompt  string            `json:"prompt"`
	Options map[string]string `json:"options"`
}

type progressPayload struct {
	Completed int `json:"completed"`
	Total     int `json:"total"`
	Remaining int `json:"remaining"`
	Attempted int `json:"attempted"`
}

type answerRequest struct {
	Answer string `json:"answer"`
}

type answerResponse struct {
	Result        quiz.Result     `json:"result"`
	Finished      bool            `json:"finished"`
	CorrectAnswer string          `json:"correctAnswer"`
	Progress      progressPayload `json:"progress"`
}

type summaryPayload struct {
	Score    int          `json:"score"`
	Answered int          `json:"answered"`
	Total    int          `json:"total"`
	Percent  float64      `json:"percent"`
	Rows     []summaryRow `json:"rows"`
}

type summaryRow struct {
	Index         int    `json:"index"`
	Correct       bool   `json:"correct"`
	UserAnswer    string `json:"userAnswer"`
	CorrectAnswer string `json:"correctAnswer"`
}

type jumpRequest struct {
	Term string `json:"term"`
}

type jumpResponse struct {
	Found  bool   `json:"found"`
	Index  int    `json:"index,omitempty"`
	Domain int    `json:"domain,omitempty"`
	Prompt string `json:"prompt,omitempty"`
}

func (s *Server) handleHome(w http.ResponseWriter, r *http.Request) {
	t := template.Must(template.New("home").Parse(indexHTML))
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = t.Execute(w, nil)
}

func (s *Server) handleState(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	session := s.session
	s.mu.Unlock()

	completed, total := session.Progress()
	attempted := session.AttemptedCount()
	idx, q, ok := session.Current()
	resp := stateResponse{
		Progress: progressPayload{
			Completed: completed,
			Total:     total,
			Remaining: total - completed,
			Attempted: attempted,
		},
	}
	if !ok {
		summary := s.buildSummary()
		resp.Finished = true
		resp.Summary = &summary
		writeJSON(w, resp)
		return
	}
	resp.Question = &questionPayload{
		Index:   idx,
		Domain:  q.Domain,
		Prompt:  q.Prompt,
		Options: q.Options,
	}
	writeJSON(w, resp)
}

func (s *Server) handleAnswer(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	s.mu.Lock()
	session := s.session
	s.mu.Unlock()
	var req answerRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	_, q, ok := session.Current()
	if !ok {
		writeJSON(w, answerResponse{Finished: true})
		return
	}
	res, finished, _ := session.Answer(req.Answer)
	completed, total := session.Progress()
	resp := answerResponse{
		Result:        res,
		Finished:      finished,
		CorrectAnswer: q.Answer,
		Progress: progressPayload{
			Completed: completed,
			Total:     total,
			Remaining: total - completed,
			Attempted: session.AttemptedCount(),
		},
	}
	writeJSON(w, resp)
}

func (s *Server) handleSummary(w http.ResponseWriter, r *http.Request) {
	summary := s.buildSummary()
	writeJSON(w, summary)
}

func (s *Server) handleReset(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	s.mu.Lock()
	s.session = quiz.NewSession(s.questions)
	s.mu.Unlock()
	writeJSON(w, map[string]string{"status": "reset"})
}

func (s *Server) handleJump(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var req jumpRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	term := strings.TrimSpace(req.Term)
	if term == "" {
		writeJSON(w, jumpResponse{Found: false})
		return
	}
	s.mu.Lock()
	session := s.session
	s.mu.Unlock()
	if session.Completed() {
		writeJSON(w, jumpResponse{Found: false})
		return
	}
	idx := s.findQuestionIndex(term)
	if idx < 0 {
		writeJSON(w, jumpResponse{Found: false})
		return
	}
	session.BringToFront(idx)
	q := s.questions[idx]
	writeJSON(w, jumpResponse{
		Found:  true,
		Index:  idx + 1,
		Domain: q.Domain,
		Prompt: q.Prompt,
	})
}

func (s *Server) buildSummary() summaryPayload {
	s.mu.Lock()
	session := s.session
	s.mu.Unlock()
	score, answered := session.Score()
	results := session.Results()
	rows := make([]summaryRow, 0, len(results))
	for i, res := range results {
		rows = append(rows, summaryRow{
			Index:         i + 1,
			Correct:       res.Correct,
			UserAnswer:    res.UserAnswer,
			CorrectAnswer: session.Questions[i].Answer,
		})
	}
	total := len(results)
	percent := 0.0
	if answered > 0 {
		percent = float64(score) * 100 / float64(answered)
	}
	return summaryPayload{
		Score:    score,
		Answered: answered,
		Total:    total,
		Percent:  percent,
		Rows:     rows,
	}
}

func (s *Server) findQuestionIndex(term string) int {
	if n, err := strconv.Atoi(term); err == nil {
		n-- // convert to 0-based
		if n >= 0 && n < len(s.questions) {
			return n
		}
	}
	needle := strings.ToLower(term)
	for i, q := range s.questions {
		if strings.Contains(strings.ToLower(q.Prompt), needle) {
			return i
		}
	}
	return -1
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}

const indexHTML = `<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>Quiz Dashboard</title>
  <link rel="icon" href="data:image/svg+xml,%3Csvg xmlns='http://www.w3.org/2000/svg' viewBox='0 0 64 64'%3E%3Crect width='64' height='64' rx='12' fill='%230f172a'/%3E%3ClinearGradient id='g' x1='0' y1='0' x2='1' y2='1'%3E%3Cstop stop-color='%2322d3ee'/%3E%3Cstop offset='1' stop-color='%23f97316'/%3E%3C/linearGradient%3E%3Cpath fill='url(%23g)' d='M32 10c-11 0-18 7-18 17 0 10 7 17 17 17 4 0 7-1 9-3l5 5c1 1 3 1 4 0 1-1 1-3 0-4l-5-5c2-3 3-6 3-10 0-10-7-17-15-17Zm0 8c5 0 8 3 8 8s-3 9-8 9-9-4-9-9 4-8 9-8Z'/%3E%3C/svg%3E">
  <style>
    :root {
      --bg: radial-gradient(120% 120% at 15% 20%, rgba(0, 195, 255, 0.18), transparent 50%),
             radial-gradient(100% 100% at 80% 0%, rgba(255, 151, 94, 0.18), transparent 40%),
             #0f172a;
      --panel: rgba(255, 255, 255, 0.04);
      --panel-strong: rgba(255, 255, 255, 0.1);
      --text: #e2e8f0;
      --muted: #94a3b8;
      --accent: #22d3ee;
      --accent-2: #f97316;
      --good: #34d399;
      --bad: #f43f5e;
      --shadow: 0 25px 60px rgba(0,0,0,0.35);
      --radius: 18px;
      font-family: "Space Grotesk", "Segoe UI", "Helvetica Neue", sans-serif;
    }
    * { box-sizing: border-box; }
    body {
      margin: 0;
      min-height: 100vh;
      background: var(--bg);
      color: var(--text);
      display: flex;
      align-items: center;
      justify-content: center;
      padding: 32px 16px;
    }
    .shell {
      width: min(960px, 100%);
      background: var(--panel);
      border: 1px solid rgba(255,255,255,0.06);
      border-radius: var(--radius);
      padding: 28px;
      box-shadow: var(--shadow);
      backdrop-filter: blur(10px);
    }
    header {
      display: flex;
      align-items: center;
      justify-content: space-between;
      gap: 16px;
      margin-bottom: 20px;
    }
    .header-actions {
      display: flex;
      align-items: center;
      gap: 10px;
    }
    .title {
      font-size: 28px;
      font-weight: 700;
      letter-spacing: 0.3px;
    }
    .badge {
      padding: 8px 14px;
      background: linear-gradient(120deg, rgba(34,211,238,0.2), rgba(249,115,22,0.2));
      border: 1px solid rgba(255,255,255,0.08);
      border-radius: 999px;
      font-size: 14px;
      color: var(--accent);
    }
    .progress {
      background: var(--panel-strong);
      border-radius: 999px;
      overflow: hidden;
      height: 14px;
      position: relative;
      margin-bottom: 12px;
    }
    .progress span {
      display: block;
      height: 100%;
      width: 0;
      background: linear-gradient(120deg, var(--accent), var(--accent-2));
      box-shadow: 0 10px 25px rgba(34,211,238,0.35);
      transition: width 220ms ease;
    }
    .progress-text {
      display: flex;
      justify-content: space-between;
      color: var(--muted);
      font-size: 14px;
      margin-bottom: 18px;
    }
    .search {
      display: flex;
      gap: 10px;
      align-items: center;
      margin-bottom: 16px;
      flex-wrap: wrap;
    }
    .search input {
      flex: 1;
      min-width: 180px;
      background: rgba(255,255,255,0.04);
      border: 1px solid rgba(255,255,255,0.06);
      color: var(--text);
      border-radius: 12px;
      padding: 10px 12px;
      outline: none;
    }
    .search input:focus {
      border-color: var(--accent);
      box-shadow: 0 0 0 3px rgba(34,211,238,0.18);
    }
    .card {
      background: var(--panel-strong);
      border: 1px solid rgba(255,255,255,0.06);
      border-radius: var(--radius);
      padding: 20px;
      box-shadow: inset 0 1px 0 rgba(255,255,255,0.08);
    }
    .question {
      font-size: 22px;
      font-weight: 700;
      margin-bottom: 14px;
      line-height: 1.4;
    }
    .options {
      display: grid;
      gap: 10px;
      grid-template-columns: repeat(auto-fit, minmax(220px, 1fr));
    }
    .option {
      border-radius: 12px;
      padding: 12px 14px;
      border: 1px solid rgba(255,255,255,0.08);
      background: rgba(255,255,255,0.03);
      color: var(--text);
      display: flex;
      gap: 10px;
      align-items: center;
      cursor: pointer;
      transition: transform 120ms ease, border-color 120ms ease, background 120ms ease, box-shadow 120ms ease;
    }
    .option:hover {
      transform: translateY(-2px);
      border-color: rgba(34,211,238,0.5);
      background: rgba(34,211,238,0.08);
    }
    .option.selected {
      border-color: var(--accent);
      box-shadow: 0 8px 24px rgba(34,211,238,0.25);
      background: rgba(34,211,238,0.1);
    }
    .option.correct {
      border-color: rgba(52,211,153,0.8);
      background: rgba(52,211,153,0.12);
    }
    .option.incorrect {
      border-color: rgba(244,63,94,0.8);
      background: rgba(244,63,94,0.12);
    }
    .option input { display: none; }
    .letter {
      width: 32px;
      height: 32px;
      border-radius: 10px;
      background: rgba(34,211,238,0.25);
      color: #0b1221;
      display: inline-flex;
      align-items: center;
      justify-content: center;
      font-weight: 700;
    }
    .footer {
      display: flex;
      gap: 10px;
      align-items: center;
      margin-top: 16px;
      color: var(--muted);
    }
    .cta {
      background: linear-gradient(120deg, var(--accent), var(--accent-2));
      border: none;
      border-radius: 12px;
      color: #0b1221;
      font-weight: 700;
      padding: 12px 16px;
      cursor: pointer;
      box-shadow: 0 12px 30px rgba(34,211,238,0.35);
      transition: transform 120ms ease, box-shadow 120ms ease;
    }
    .cta.ghost {
      background: transparent;
      color: var(--accent);
      border: 1px solid rgba(34,211,238,0.5);
      box-shadow: none;
    }
    .cta.small {
      padding: 8px 12px;
    }
    .cta:hover {
      transform: translateY(-1px);
      box-shadow: 0 16px 38px rgba(34,211,238,0.5);
    }
    .pill {
      padding: 8px 12px;
      border-radius: 999px;
      font-weight: 600;
      font-size: 14px;
    }
    .pill.good { background: rgba(52,211,153,0.15); color: #34d399; }
    .pill.bad { background: rgba(244,63,94,0.15); color: #f871a6; }
    .muted { color: var(--muted); }
    .good { color: var(--good); }
    .bad { color: var(--bad); }
    .summary {
      display: grid;
      gap: 10px;
      margin-top: 12px;
    }
    .summary-row {
      display: flex;
      justify-content: space-between;
      padding: 10px 12px;
      border-radius: 10px;
      background: rgba(255,255,255,0.03);
      border: 1px solid rgba(255,255,255,0.06);
      font-size: 14px;
    }
    .modal {
      position: fixed;
      inset: 0;
      background: rgba(6,9,19,0.65);
      backdrop-filter: blur(4px);
      display: flex;
      align-items: center;
      justify-content: center;
      z-index: 10;
    }
    .modal.hidden { display: none; }
    .modal-content {
      width: min(620px, 96%);
      background: var(--panel);
      border: 1px solid rgba(255,255,255,0.08);
      border-radius: 16px;
      padding: 20px;
      box-shadow: var(--shadow);
      max-height: 80vh;
      overflow: auto;
    }
    .modal-actions {
      display: flex;
      justify-content: flex-end;
      gap: 10px;
      margin-top: 14px;
    }
    @media (max-width: 640px) {
      .shell { padding: 20px; }
      header { flex-direction: column; align-items: flex-start; }
      .question { font-size: 20px; }
      .search { flex-direction: column; align-items: stretch; }
      .search .pill { width: 100%; text-align: center; }
    }
  </style>
</head>
<body>
  <div class="shell">
    <header>
      <div class="title">CSSLP Review Quiz</div>
      <div class="header-actions">
        <div class="badge" id="statusBadge">CLI heritage · now on the web</div>
        <button class="cta ghost small" id="resetBtn" aria-label="Reset quiz">Try Again</button>
      </div>
    </header>
    <div class="progress"><span id="progressBar"></span></div>
    <div class="progress-text">
      <div id="progressLabel">0% complete</div>
      <div id="progressCounts">0 / 0</div>
    </div>
    <div class="search">
      <input id="searchTerm" type="search" placeholder="Search question text or number..." aria-label="Search question" />
      <button class="cta ghost" id="searchBtn">Search & Jump</button>
      <div id="searchFeedback" class="pill muted">Search to jump to a question.</div>
    </div>
    <div class="card" id="card">
      <div class="question" id="prompt">Loading question...</div>
      <div class="options" id="options"></div>
      <div class="footer">
        <div id="feedback" class="pill muted">Pick an answer to begin.</div>
        <button class="cta" id="actionBtn">Submit</button>
      </div>
    </div>
    <div class="card" id="summary" style="display:none;">
      <div class="question">Quiz Complete</div>
      <div id="scoreLine" class="muted"></div>
      <div class="summary" id="summaryRows"></div>
      <button class="cta" id="summaryResetBtn">Try Again</button>
    </div>
  </div>
  <div class="modal hidden" id="partialModal" role="dialog" aria-modal="true" aria-labelledby="partialTitle">
    <div class="modal-content">
      <div class="question" id="partialTitle">Partial Grade</div>
      <div id="partialScoreLine" class="muted"></div>
      <div class="summary scrollable" id="partialRows"></div>
      <div class="modal-actions">
        <button class="cta ghost" id="cancelPartial">Keep going</button>
        <button class="cta" id="readyBtn">Ready!</button>
      </div>
    </div>
  </div>
  <script>
    let selected = "";
    let lock = false;
    let optionNodes = {};
    const FEEDBACK_PAUSE = 1400;
    const searchInput = document.getElementById("searchTerm");
    const searchFeedback = document.getElementById("searchFeedback");
    const partialModal = document.getElementById("partialModal");
    const partialRows = document.getElementById("partialRows");
    const partialScoreLine = document.getElementById("partialScoreLine");

    function optionTemplate(letter, text) {
      return '<label class="option">' +
        '<span class="letter">' + letter + '</span>' +
        '<input type="radio" name="option" value="' + letter + '">' +
        '<span>' + text + '</span>' +
        '</label>';
    }

    async function loadState() {
      const res = await fetch("/api/state");
      const data = await res.json();
      updateProgress(data.progress);
      if (data.finished) {
        showSummary(data.summary);
        return;
      }
      renderQuestion(data.question);
    }

    function renderRows(rows, target, emptyText = "") {
      target.innerHTML = "";
      if (!rows || rows.length === 0) {
        if (emptyText) {
          const div = document.createElement("div");
          div.className = "summary-row";
          div.innerText = emptyText;
          target.appendChild(div);
        }
        return;
      }
      rows.forEach(row => {
        const div = document.createElement("div");
        const emoji = row.correct ? "✅" : "❌";
        const tone = row.correct ? "good" : "bad";
        div.className = "summary-row";
        div.innerHTML = '<span>' + emoji + ' Q' + row.index + '</span><span class="' + (tone === "good" ? "good" : "bad") + '">You: ' + (row.userAnswer || "–") + ' · Correct: ' + row.correctAnswer + '</span>';
        target.appendChild(div);
      });
    }

    function setSearchStatus(text, tone = "muted") {
      searchFeedback.innerText = text;
      const toneClass = tone === "good" ? "pill good" : tone === "bad" ? "pill bad" : "pill muted";
      searchFeedback.className = toneClass;
    }

    function renderQuestion(q) {
      selected = "";
      lock = false;
      optionNodes = {};
      document.getElementById("feedback").className = "pill muted";
      document.getElementById("feedback").innerText = "Choose an option.";
      const qNumber = (q.index ?? 0) + 1;
      document.getElementById("prompt").innerText = "Q" + qNumber + " · Domain " + q.domain + " · " + q.prompt;
      const opts = document.getElementById("options");
      opts.innerHTML = "";
      const letters = Object.keys(q.options).sort();
      letters.forEach(letter => {
        const node = document.createElement("div");
        node.innerHTML = optionTemplate(letter, q.options[letter]);
        const label = node.firstElementChild;
        label.dataset.letter = letter;
        label.addEventListener("click", () => selectOption(letter));
        optionNodes[letter] = label;
        opts.appendChild(label);
      });
      document.getElementById("actionBtn").innerText = "Submit";
      document.getElementById("actionBtn").onclick = submitAnswer;
      setSearchStatus("Search text or a number, then jump.", "muted");
    }

    function selectOption(letter) {
      if (lock) return;
      selected = letter;
      Object.values(optionNodes).forEach(node => {
        node.classList.toggle("selected", node.dataset.letter === letter);
      });
      const pill = document.getElementById("feedback");
      pill.className = "pill muted";
      pill.innerText = "Ready to submit " + letter + ".";
    }

    function updateProgress(p) {
      const pct = p.total === 0 ? 0 : Math.round((p.completed / p.total) * 100);
      document.getElementById("progressBar").style.width = pct + "%";
      document.getElementById("progressLabel").innerText = pct + "% complete";
      document.getElementById("progressCounts").innerText = p.completed + " of " + p.total + " correct · " + p.attempted + " attempted";
    }

    async function searchAndJump() {
      if (lock) return;
      const term = searchInput.value.trim();
      if (!term) {
        setSearchStatus("Enter text or a question number to jump.", "bad");
        return;
      }
      setSearchStatus("Searching...", "muted");
      try {
        const res = await fetch("/api/jump", {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ term })
        });
        const data = await res.json();
        if (!data.found) {
          setSearchStatus("No question matched that search.", "bad");
          return;
        }
        setSearchStatus("Jumped to Q" + data.index + " (Domain " + data.domain + ")", "good");
        selected = "";
        lock = false;
        loadState();
        searchInput.blur();
      } catch (err) {
        setSearchStatus("Search failed. Please try again.", "bad");
      }
    }

    async function submitAnswer() {
      if (lock) return;
      if (!selected) {
        const pill = document.getElementById("feedback");
        pill.innerText = "Please pick an option.";
        pill.className = "pill bad";
        return;
      }
      lock = true;
      const res = await fetch("/api/answer", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ answer: selected })
      });
      const data = await res.json();
      updateProgress(data.progress);
      const pill = document.getElementById("feedback");
      if (data.result.correct) {
        pill.innerText = "✅ Correct! Moving to the next question shortly.";
        pill.className = "pill good";
      } else {
        pill.innerText = "❌ Incorrect. Correct answer: " + data.correctAnswer + ". Take a moment - next question incoming.";
        pill.className = "pill bad";
      }
      Object.entries(optionNodes).forEach(([letter, node]) => {
        node.classList.remove("correct", "incorrect", "selected");
        if (letter === data.correctAnswer) node.classList.add("correct");
        if (letter === selected && !data.result.correct) node.classList.add("incorrect");
        if (letter === selected && data.result.correct) node.classList.add("correct");
      });
      if (data.finished) {
        setTimeout(() => loadState(), FEEDBACK_PAUSE);
      } else {
        setTimeout(() => { lock = false; loadState(); }, FEEDBACK_PAUSE);
      }
    }

    function showSummary(summary) {
      document.getElementById("card").style.display = "none";
      const summaryBox = document.getElementById("summary");
      summaryBox.style.display = "block";
      const pct = summary.answered === 0 ? 0 : (summary.score / summary.answered * 100).toFixed(1);
      document.getElementById("scoreLine").innerText = "First-attempt score: " + summary.score + "/" + summary.answered + " (" + pct + "%)";
      renderRows(summary.rows, document.getElementById("summaryRows"));
    }

    function resetPage() {
      fetch("/api/reset", { method: "POST" }).then(() => {
        selected = "";
        lock = false;
        document.getElementById("summary").style.display = "none";
        document.getElementById("card").style.display = "block";
        setSearchStatus("Session reset. Start anywhere.", "muted");
        closePartial();
        loadState();
      });
    }

    async function openPartialSummary() {
      if (lock) return;
      lock = true;
      try {
        const res = await fetch("/api/summary");
        const data = await res.json();
        const pct = data.answered === 0 ? 0 : (data.score / data.answered * 100).toFixed(1);
        partialScoreLine.innerText = data.answered === 0
          ? "No answers yet. Ready to start over?"
          : "Partial score: " + data.score + "/" + data.answered + " (" + pct + "%) so far.";
        const attemptedRows = (data.rows || []).filter(r => r.userAnswer);
        renderRows(attemptedRows, partialRows, attemptedRows.length ? "" : "No answers recorded yet.");
        partialModal.classList.remove("hidden");
      } catch (e) {
        setSearchStatus("Could not load partial grade.", "bad");
        lock = false;
      }
    }

    function closePartial() {
      partialModal.classList.add("hidden");
      lock = false;
    }

    document.getElementById("searchBtn").addEventListener("click", searchAndJump);
    searchInput.addEventListener("keydown", (e) => {
      if (e.key === "Enter") {
        e.preventDefault();
        searchAndJump();
      }
    });
    document.getElementById("resetBtn").addEventListener("click", openPartialSummary);
    document.getElementById("summaryResetBtn").addEventListener("click", openPartialSummary);
    document.getElementById("readyBtn").addEventListener("click", resetPage);
    document.getElementById("cancelPartial").addEventListener("click", closePartial);

    loadState();
  </script>
</body>
</html>`
