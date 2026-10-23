package repo

import (
	"path"
	"strings"
)

// Analysed lists the languages that at least one metric family analyses in this build. It is empty
// until the first family lands: a language is "analysed" only when something actually read it.
// Every other recognised language is reported as present but not analysed, with its counts.
var Analysed = map[string]bool{}

// extensions maps file extensions to language names. Recognising a language only means it is
// counted; analysis depends on Analysed.
var extensions = map[string]string{
	".go": "go",
	".py": "python", ".pyi": "python",
	".js": "javascript", ".mjs": "javascript", ".cjs": "javascript", ".jsx": "javascript",
	".ts": "typescript", ".mts": "typescript", ".cts": "typescript", ".tsx": "typescript",
	".rs":   "rust",
	".java": "java", ".kt": "kotlin", ".kts": "kotlin", ".scala": "scala",
	".c": "c", ".h": "c",
	".cc": "cpp", ".cpp": "cpp", ".cxx": "cpp", ".hpp": "cpp", ".hh": "cpp",
	".cs":    "csharp",
	".rb":    "ruby",
	".php":   "php",
	".swift": "swift",
	".sh":    "shell", ".bash": "shell",
}

func language(rel string) string {
	return extensions[strings.ToLower(path.Ext(rel))]
}
