# Quiz CLI

Command-line quiz runner for multiple-choice question sets stored in JSON.

## Running
- From this folder: `go run .`
- Or build a binary: `go build ./...` then run `./quiz-cli`
- Controls: use `↑/↓` then Enter to select, or type `A–D` and Enter. Press `/` to search, `Ctrl+C` to quit early (a partial grade is shown).

## Question File Format
Create a `questions.json` beside the executable. It must be a JSON array of objects with these fields:
- `domain` (number): arbitrary grouping value (shown in the UI).
- `question` (string): the prompt text.
- `options` (object): keys are option letters (A–D recommended), values are the answer texts.
- `answer` (string): the correct option key (e.g., `"C"`).

Example:
```json
[
  {
    "domain": 1,
    "question": "What color is the sky on a clear day?",
    "options": {
      "A": "Green",
      "B": "Blue",
      "C": "Red",
      "D": "Purple"
    },
    "answer": "B"
  }
]
```

Notes:
- Only the first character of `answer` is used; keep it aligned with an option key.
- Options are rendered alphabetically by their keys; stick to single-letter keys for clarity.
