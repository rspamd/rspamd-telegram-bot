package telegram

import (
	"fmt"
	"html"
	"sort"
	"strings"
	"unicode/utf16"

	"github.com/go-telegram/bot/models"
)

// entitiesToHTML converts Telegram message text + entities to HTML.
// Telegram uses UTF-16 offsets, so we convert accordingly.
func entitiesToHTML(text string, entities []models.MessageEntity) string {
	if len(entities) == 0 {
		return html.EscapeString(text)
	}

	// Convert text to UTF-16 for correct offset handling
	runes := []rune(text)
	utf16Units := utf16.Encode(runes)

	// Sort entities by offset (should already be sorted, but be safe)
	sorted := make([]models.MessageEntity, len(entities))
	copy(sorted, entities)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].Offset < sorted[j].Offset
	})

	// Build HTML by walking through UTF-16 units
	var sb strings.Builder
	pos := 0 // current position in UTF-16 units

	for _, ent := range sorted {
		// Append text before this entity
		if ent.Offset > pos {
			sb.WriteString(html.EscapeString(utf16ToString(utf16Units[pos:ent.Offset])))
		}

		// Extract entity text
		end := ent.Offset + ent.Length
		if end > len(utf16Units) {
			end = len(utf16Units)
		}
		entityText := html.EscapeString(utf16ToString(utf16Units[ent.Offset:end]))

		// Wrap with HTML tags
		switch ent.Type {
		case models.MessageEntityTypeBold:
			sb.WriteString("<b>" + entityText + "</b>")
		case models.MessageEntityTypeItalic:
			sb.WriteString("<i>" + entityText + "</i>")
		case models.MessageEntityTypeUnderline:
			sb.WriteString("<u>" + entityText + "</u>")
		case models.MessageEntityTypeStrikethrough:
			sb.WriteString("<s>" + entityText + "</s>")
		case models.MessageEntityTypeCode:
			sb.WriteString("<code>" + entityText + "</code>")
		case models.MessageEntityTypePre:
			if ent.Language != "" {
				sb.WriteString(fmt.Sprintf("<pre><code class=\"language-%s\">%s</code></pre>",
					html.EscapeString(ent.Language), entityText))
			} else {
				sb.WriteString("<pre>" + entityText + "</pre>")
			}
		case models.MessageEntityTypeTextLink:
			sb.WriteString(fmt.Sprintf("<a href=\"%s\">%s</a>",
				html.EscapeString(ent.URL), entityText))
		case models.MessageEntityTypeURL:
			sb.WriteString(fmt.Sprintf("<a href=\"%s\">%s</a>", entityText, entityText))
		case models.MessageEntityTypeMention:
			sb.WriteString(fmt.Sprintf("<a href=\"https://t.me/%s\">%s</a>",
				html.EscapeString(strings.TrimPrefix(entityText, "@")), entityText))
		case models.MessageEntityTypeEmail:
			sb.WriteString(fmt.Sprintf("<a href=\"mailto:%s\">%s</a>", entityText, entityText))
		case models.MessageEntityTypeTextMention:
			if ent.User != nil {
				sb.WriteString(fmt.Sprintf("<a href=\"tg://user?id=%d\">%s</a>",
					ent.User.ID, entityText))
			} else {
				sb.WriteString(entityText)
			}
		case models.MessageEntityTypeBlockquote, models.MessageEntityTypeExpandableBlockquote:
			sb.WriteString("<blockquote>" + entityText + "</blockquote>")
		case models.MessageEntityTypeSpoiler:
			sb.WriteString("<span class=\"spoiler\">" + entityText + "</span>")
		default:
			sb.WriteString(entityText)
		}

		pos = end
	}

	// Append remaining text
	if pos < len(utf16Units) {
		sb.WriteString(html.EscapeString(utf16ToString(utf16Units[pos:])))
	}

	return sb.String()
}

func utf16ToString(units []uint16) string {
	runes := utf16.Decode(units)
	return string(runes)
}
