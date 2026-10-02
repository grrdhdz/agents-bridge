package control

import "regexp"

var capabilityPattern = regexp.MustCompile(`[A-Za-z0-9_-]{43}`)
var bridgeTokenPattern = regexp.MustCompile(`[a-z2-7]{32}`)
var controlURLPattern = regexp.MustCompile(`http://(?:127\.0\.0\.1|localhost):[0-9]+`)

// Bridge credentials have fixed encodings. Match whole tokens, so a long
// ordinary word or hash is not repeatedly chopped into secret-sized chunks.
func RedactText(text string) string {
	for _, pattern := range []*regexp.Regexp{capabilityPattern, bridgeTokenPattern} {
		matches := pattern.FindAllStringIndex(text, -1)
		for i := len(matches) - 1; i >= 0; i-- {
			start, end := matches[i][0], matches[i][1]
			if (start > 0 && encodedByte(text[start-1])) || (end < len(text) && encodedByte(text[end])) {
				continue
			}
			text = text[:start] + "[credencial omitida]" + text[end:]
		}
	}
	return controlURLPattern.ReplaceAllString(text, "[ruta de control omitida]")
}
func encodedByte(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_' || c == '-'
}
