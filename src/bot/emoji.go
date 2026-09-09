package bot

import (
	"maps"
	"sync"

	"github.com/go-faster/errors"
	"github.com/gotd/td/tg"
)

// customEmojiCache remembers the base emoji for each custom emoji document.
// Entries are kept for the lifetime of the process: a document's base emoji
// never changes.
type customEmojiCache struct {
	mu      sync.Mutex
	byDocID map[int64]string
}

func newCustomEmojiCache() *customEmojiCache {
	return &customEmojiCache{byDocID: make(map[int64]string)}
}

// resolve returns the base emoji for each document ID, invoking fetch only
// for the IDs not yet cached (deduplicated).
func (c *customEmojiCache) resolve(ids []int64, fetch func(missing []int64) (map[int64]string, error)) (map[int64]string, error) {
	resolved := make(map[int64]string, len(ids))
	var missing []int64
	seen := make(map[int64]bool, len(ids))

	c.mu.Lock()
	for _, id := range ids {
		if alt, ok := c.byDocID[id]; ok {
			resolved[id] = alt
			continue
		}
		if !seen[id] {
			seen[id] = true
			missing = append(missing, id)
		}
	}
	c.mu.Unlock()

	if len(missing) == 0 {
		return resolved, nil
	}

	fetched, err := fetch(missing)
	if err != nil {
		return nil, err
	}

	c.mu.Lock()
	maps.Copy(c.byDocID, fetched)
	c.mu.Unlock()
	maps.Copy(resolved, fetched)

	return resolved, nil
}

// GetCustomEmoji returns the base emoji associated with each custom emoji document.
func (b *Bot) GetCustomEmoji(documentIDs []int64) (map[int64]string, error) {
	return b.emojiCache.resolve(documentIDs, func(missing []int64) (map[int64]string, error) {
		documents, err := b.api.MessagesGetCustomEmojiDocuments(b.ctx, missing)
		if err != nil {
			return nil, errors.Wrap(err, "getting custom emoji documents")
		}

		fetched := make(map[int64]string, len(documents))
		for _, document := range documents {
			doc, ok := document.(*tg.Document)
			if !ok {
				continue
			}
			for _, attribute := range doc.Attributes {
				if custom, ok := attribute.(*tg.DocumentAttributeCustomEmoji); ok {
					fetched[doc.ID] = custom.Alt
					break
				}
			}
		}
		return fetched, nil
	})
}
