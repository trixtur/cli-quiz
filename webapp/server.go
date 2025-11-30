package webapp

import (
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
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
      transition: transform 120ms ease, border-color 120ms ease, background 120ms ease;
    }
    .option:hover {
      transform: translateY(-2px);
      border-color: rgba(34,211,238,0.5);
      background: rgba(34,211,238,0.08);
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
    @media (max-width: 640px) {
      .shell { padding: 20px; }
      header { flex-direction: column; align-items: flex-start; }
      .question { font-size: 20px; }
    }
  </style>
</head>
<body>
  <div class="shell">
    <header>
      <div class="title">CSSLP Review Quiz</div>
      <div class="badge" id="statusBadge">CLI heritage · now on the web</div>
    </header>
    <div class="progress"><span id="progressBar"></span></div>
    <div class="progress-text">
      <div id="progressLabel">0% complete</div>
      <div id="progressCounts">0 / 0</div>
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
      <button class="cta" onclick="resetPage()">Try Again</button>
    </div>
  </div>
  <script>
    let selected = "";
    let lock = false;

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

    function renderQuestion(q) {
      selected = "";
      lock = false;
      document.getElementById("feedback").className = "pill muted";
      document.getElementById("feedback").innerText = "Choose an option.";
      document.getElementById("prompt").innerText = "Domain " + q.domain + " · " + q.prompt;
      const opts = document.getElementById("options");
      opts.innerHTML = "";
      const letters = Object.keys(q.options).sort();
      letters.forEach(letter => {
        const node = document.createElement("div");
        node.innerHTML = optionTemplate(letter, q.options[letter]);
        const label = node.firstElementChild;
        label.addEventListener("click", () => { selected = letter; });
        opts.appendChild(label);
      });
      document.getElementById("actionBtn").innerText = "Submit";
      document.getElementById("actionBtn").onclick = submitAnswer;
    }

    function updateProgress(p) {
      const pct = p.total === 0 ? 0 : Math.round((p.completed / p.total) * 100);
      document.getElementById("progressBar").style.width = pct + "%";
      document.getElementById("progressLabel").innerText = pct + "% complete";
      document.getElementById("progressCounts").innerText = p.completed + " of " + p.total + " correct · " + p.attempted + " attempted";
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
        pill.innerText = "✅ Correct!";
        pill.className = "pill good";
      } else {
        pill.innerText = "❌ Incorrect. Correct answer: " + data.correctAnswer;
        pill.className = "pill bad";
      }
      if (data.finished) {
        setTimeout(() => loadState(), 350);
      } else {
        setTimeout(() => { lock = false; loadState(); }, 350);
      }
    }

    function showSummary(summary) {
      document.getElementById("card").style.display = "none";
      const summaryBox = document.getElementById("summary");
      summaryBox.style.display = "block";
      const pct = summary.answered === 0 ? 0 : (summary.score / summary.answered * 100).toFixed(1);
      document.getElementById("scoreLine").innerText = "First-attempt score: " + summary.score + "/" + summary.answered + " (" + pct + "%)";
      const rows = document.getElementById("summaryRows");
      rows.innerHTML = "";
      summary.rows.forEach(row => {
        const div = document.createElement("div");
        const emoji = row.correct ? "✅" : "❌";
        const tone = row.correct ? "good" : "bad";
        div.className = "summary-row";
        div.innerHTML = '<span>' + emoji + ' Q' + row.index + '</span><span class="' + (tone === "good" ? "good" : "bad") + '">You: ' + (row.userAnswer || "–") + ' · Correct: ' + row.correctAnswer + '</span>';
        rows.appendChild(div);
      });
    }

    function resetPage() {
      fetch("/api/reset", { method: "POST" }).then(() => {
        selected = "";
        lock = false;
        document.getElementById("summary").style.display = "none";
        document.getElementById("card").style.display = "block";
        loadState();
      });
    }

    loadState();
  </script>
</body>
</html>`
