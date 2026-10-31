package study

import "strings"

// validateTaskContentType enforces the task-level content type. Video and audio
// tasks must carry a matching uploaded asset; article and carousel tasks must
// resolve to that kind. An empty content type accepts any kind.
func validateTaskContentType(contentType string, items []Content) error {
	switch contentType {
	case "":
		return nil
	case "video", "audio":
		prefix := contentType + "/"
		for _, item := range items {
			matched := false
			for _, asset := range item.Assets {
				if strings.HasPrefix(strings.ToLower(asset.MIME), prefix) {
					matched = true
					break
				}
			}
			if !matched {
				return ErrContentType
			}
		}
		return nil
	case "article", "carousel":
		for _, item := range items {
			if Kind(item) != contentType {
				return ErrContentType
			}
		}
		return nil
	default:
		return ErrInvalid
	}
}
