package repo

import (
	"path"
	"strings"
)

// extensions maps file extensions to language names. Recognising a language only means it is
// counted; whether it is analysed depends on package lang.
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
