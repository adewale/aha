package testquality_test

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var americanProse = regexp.MustCompile(`(?i)\b(behaviors?|analyz(e[drs]?|ing)|analyzers?|artifacts?|authori(zation|zed|ze)|initiali(ze[drs]?|zing|zation)|optimi(ze[drs]?|zing|zation|zations)|recogni(ze[drs]?|zing|zable)|saniti(ze[drs]?|zing|zed)|colors?|centers?|centered|centering|favors?|favored|favoring|honors?|honored|honoring|licenses?|catalogs?|cataloged|cataloging|organizations?|organi(ze[drs]?|zing|zed)|modeling|labeled|labeling|cancelation)\b`)
var inlineCodeOrURL = regexp.MustCompile("`+[^`]*`+|https?://[^\\s)]+")
var htmlStyle = regexp.MustCompile(`(?is)<style\b[^>]*>.*?</style>`)
var htmlScript = regexp.MustCompile(`(?is)<script\b[^>]*>.*?</script>`)
var htmlCode = regexp.MustCompile(`(?is)<code\b[^>]*>.*?</code>`)
var htmlTags = regexp.MustCompile(`(?s)<[^>]+>`)

func TestArchitectureHTMLVisibleCopyUsesBritishEnglish(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("..", "..", "docs", "interactive", "architecture.html"))
	if err != nil {
		t.Fatal(err)
	}
	prose := htmlStyle.ReplaceAllString(string(body), " ")
	prose = htmlScript.ReplaceAllString(prose, " ")
	prose = htmlCode.ReplaceAllString(prose, " ")
	prose = htmlTags.ReplaceAllString(prose, " ")
	// These are visible legacy machine-contract identifiers, not prose.
	prose = strings.NewReplacer("artifact:v1", "", "artifacts", "", "artifact", "").Replace(prose)
	if match := americanProse.FindString(prose); match != "" {
		t.Fatalf("architecture.html visible copy uses American-English prose %q", match)
	}
}

func TestFirstPartyMarkdownUsesBritishEnglish(t *testing.T) {
	root := filepath.Join("..", "..")
	err := filepath.Walk(root, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if info.IsDir() {
			base := filepath.Base(path)
			if base == ".git" || base == "node_modules" || base == ".pi-subagents" || path == filepath.Join(root, "docs", "research") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".md") {
			return nil
		}
		file, err := os.Open(path)
		if err != nil {
			return err
		}
		defer file.Close()
		fenced := false
		scanner := bufio.NewScanner(file)
		lineNo := 0
		for scanner.Scan() {
			lineNo++
			line := scanner.Text()
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
				fenced = !fenced
				continue
			}
			if fenced {
				continue
			}
			prose := inlineCodeOrURL.ReplaceAllString(line, "")
			if match := americanProse.FindString(prose); match != "" {
				t.Errorf("%s:%d uses American-English prose %q", path, lineNo, match)
			}
		}
		return scanner.Err()
	})
	if err != nil {
		t.Fatal(err)
	}
}
