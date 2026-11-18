package open5e

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// DefaultBaseURL is the public Open5e API root. No API key is needed.
const DefaultBaseURL = "https://api.open5e.com"

// PageSize is how many monsters one bestiary page shows.
const PageSize = 20

// maxBodyBytes caps how much of a response is read, so a misbehaving endpoint
// cannot exhaust memory.
const maxBodyBytes = 8 << 20

// ErrNotFound is returned when the API answers 404, which for this client
// means "no such monster, weapon or spell".
var ErrNotFound = errors.New("not found")

// Client talks to the Open5e API. Every response passes through the cache, so
// repeated fights against the same foe cost one request per TTL window.
type Client struct {
	base  *url.URL
	http  *http.Client
	cache *Cache

	// prefetchLimit bounds how many requests the concurrent prefetcher makes
	// at once, to stay polite to a free public API.
	prefetchLimit int
}

// Option configures a Client.
type Option func(*Client)

// WithBaseURL points the client at another root, which is how the tests aim it
// at an httptest.Server.
func WithBaseURL(raw string) Option {
	return func(c *Client) {
		if u, err := url.Parse(strings.TrimRight(raw, "/")); err == nil && u.Host != "" {
			c.base = u
		}
	}
}

// WithHTTPClient replaces the underlying HTTP client.
func WithHTTPClient(h *http.Client) Option {
	return func(c *Client) {
		if h != nil {
			c.http = h
		}
	}
}

// WithCache replaces the response cache.
func WithCache(cache *Cache) Option {
	return func(c *Client) {
		if cache != nil {
			c.cache = cache
		}
	}
}

// WithPrefetchLimit bounds the concurrent prefetcher.
func WithPrefetchLimit(n int) Option {
	return func(c *Client) {
		if n > 0 {
			c.prefetchLimit = n
		}
	}
}

// NewClient builds a client. Without options it talks to the public API with a
// five minute cache and a fifteen second timeout per request.
func NewClient(opts ...Option) *Client {
	base, err := url.Parse(DefaultBaseURL)
	if err != nil {
		// DefaultBaseURL is a constant, so this cannot happen.
		panic(fmt.Sprintf("open5e: bad default base URL: %v", err))
	}
	c := &Client{
		base:          base,
		http:          &http.Client{Timeout: 15 * time.Second},
		cache:         NewCache(DefaultTTL, DefaultReapInterval),
		prefetchLimit: 4,
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// Cache exposes the response cache, for the cache command.
func (c *Client) Cache() *Cache { return c.cache }

// Close stops the cache reaper goroutine.
func (c *Client) Close() {
	if c.cache != nil {
		c.cache.Stop()
	}
}

// endpoint builds an absolute URL under the client base.
func (c *Client) endpoint(path string, query url.Values) string {
	u := *c.base
	u.Path = strings.TrimRight(u.Path, "/") + path
	u.RawQuery = query.Encode()
	return u.String()
}

// get fetches a URL, serving it from the cache when a fresh copy is held.
func (c *Client) get(ctx context.Context, raw string) ([]byte, error) {
	if body, ok := c.cache.Get(raw); ok {
		return body, nil
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return nil, fmt.Errorf("build request for %s: %w", raw, err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "grimhold/1.0 (+https://open5e.com)")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("get %s: %w", raw, err)
	}
	defer resp.Body.Close()

	switch {
	case resp.StatusCode == http.StatusNotFound:
		return nil, fmt.Errorf("get %s: %w", raw, ErrNotFound)
	case resp.StatusCode != http.StatusOK:
		return nil, fmt.Errorf("get %s: unexpected status %s", raw, resp.Status)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", raw, err)
	}
	c.cache.Put(raw, body)
	return body, nil
}

// getJSON fetches a URL and decodes it into out.
func getJSON[T any](ctx context.Context, c *Client, raw string, out *T) error {
	body, err := c.get(ctx, raw)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("decode %s: %w", raw, err)
	}
	return nil
}

// sameHost guards against following a pagination URL that points somewhere
// else: next and previous come from the response body, not from us.
func (c *Client) sameHost(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	return strings.EqualFold(u.Host, c.base.Host)
}

// Monster fetches one monster by slug, for example "goblin".
func (c *Client) Monster(ctx context.Context, slug string) (*Monster, error) {
	slug = Slugify(slug)
	if slug == "" {
		return nil, fmt.Errorf("monster: %w: empty name", ErrNotFound)
	}
	var m Monster
	if err := getJSON(ctx, c, c.endpoint("/v1/monsters/"+slug+"/", nil), &m); err != nil {
		return nil, fmt.Errorf("monster %q: %w", slug, err)
	}
	if m.Name == "" {
		// A 200 with no name is not a monster: treat it as a miss rather than
		// handing the game an empty stat block.
		return nil, fmt.Errorf("monster %q: %w", slug, ErrNotFound)
	}
	return &m, nil
}

// Monsters fetches a page of the bestiary. Pass an empty pageURL for the first
// page, or the Next/Previous URL of a page already held.
func (c *Client) Monsters(ctx context.Context, pageURL string) (*Page[Monster], error) {
	if pageURL == "" {
		pageURL = c.endpoint("/v1/monsters/", url.Values{
			"limit":    {fmt.Sprint(PageSize)},
			"ordering": {"cr"},
		})
	} else if !c.sameHost(pageURL) {
		return nil, fmt.Errorf("bestiary page %q: refusing to follow a URL outside %s", pageURL, c.base.Host)
	}

	var page Page[Monster]
	if err := getJSON(ctx, c, pageURL, &page); err != nil {
		return nil, fmt.Errorf("bestiary page: %w", err)
	}
	return &page, nil
}

// SearchMonsters looks monsters up by name fragment.
func (c *Client) SearchMonsters(ctx context.Context, query string, limit int) ([]Monster, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, errors.New("search: empty query")
	}
	if limit <= 0 {
		limit = 10
	}
	raw := c.endpoint("/v1/monsters/", url.Values{
		"search": {query},
		"limit":  {fmt.Sprint(limit)},
	})
	var page Page[Monster]
	if err := getJSON(ctx, c, raw, &page); err != nil {
		return nil, fmt.Errorf("search %q: %w", query, err)
	}
	return page.Results, nil
}

// Weapons fetches the weapon list, used by the loot and equip commands.
func (c *Client) Weapons(ctx context.Context) ([]Weapon, error) {
	raw := c.endpoint("/v1/weapons/", url.Values{"limit": {"100"}})
	var page Page[Weapon]
	if err := getJSON(ctx, c, raw, &page); err != nil {
		return nil, fmt.Errorf("weapons: %w", err)
	}
	return page.Results, nil
}

// Spell fetches one spell by slug.
func (c *Client) Spell(ctx context.Context, slug string) (*Spell, error) {
	slug = Slugify(slug)
	if slug == "" {
		return nil, fmt.Errorf("spell: %w: empty name", ErrNotFound)
	}
	var s Spell
	if err := getJSON(ctx, c, c.endpoint("/v1/spells/"+slug+"/", nil), &s); err != nil {
		return nil, fmt.Errorf("spell %q: %w", slug, err)
	}
	if s.Name == "" {
		return nil, fmt.Errorf("spell %q: %w", slug, ErrNotFound)
	}
	return &s, nil
}

// PrefetchMonsters warms the cache for several slugs at once. Errors are
// deliberately dropped: a prefetch is an optimisation, and the command that
// actually needs the monster will report any failure itself.
//
// This is what makes travel feel fast: by the time the player explores, the
// region's foes are already in the cache.
func (c *Client) PrefetchMonsters(ctx context.Context, slugs []string) {
	if len(slugs) == 0 {
		return
	}
	limit := c.prefetchLimit
	if limit <= 0 {
		limit = 4
	}

	seen := make(map[string]bool, len(slugs))
	sem := make(chan struct{}, limit)
	var wg sync.WaitGroup

	for _, slug := range slugs {
		slug = Slugify(slug)
		if slug == "" || seen[slug] {
			continue
		}
		seen[slug] = true

		wg.Add(1)
		go func(slug string) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				return
			}
			_, _ = c.Monster(ctx, slug)
		}(slug)
	}
	wg.Wait()
}

// MonstersConcurrently fetches several monsters at once and returns the ones
// that came back, in no particular order. It is used by explore, which draws
// more than one candidate foe in a dangerous region.
func (c *Client) MonstersConcurrently(ctx context.Context, slugs []string) ([]*Monster, error) {
	if len(slugs) == 0 {
		return nil, errors.New("no monsters requested")
	}

	type result struct {
		monster *Monster
		err     error
	}
	results := make([]result, len(slugs))
	sem := make(chan struct{}, c.prefetchLimit)
	var wg sync.WaitGroup

	for i, slug := range slugs {
		wg.Add(1)
		go func(i int, slug string) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				results[i] = result{err: ctx.Err()}
				return
			}
			m, err := c.Monster(ctx, slug)
			results[i] = result{monster: m, err: err}
		}(i, slug)
	}
	wg.Wait()

	var out []*Monster
	var firstErr error
	for _, r := range results {
		switch {
		case r.err != nil && firstErr == nil:
			firstErr = r.err
		case r.monster != nil:
			out = append(out, r.monster)
		}
	}
	if len(out) == 0 {
		if firstErr == nil {
			firstErr = ErrNotFound
		}
		return nil, firstErr
	}
	return out, nil
}

// SpellsConcurrently fetches several spells at once, returning the ones that
// came back. Character creation uses it so that offering a spell list costs
// one round trip rather than nine.
func (c *Client) SpellsConcurrently(ctx context.Context, slugs []string) []*Spell {
	if len(slugs) == 0 {
		return nil
	}
	out := make([]*Spell, len(slugs))
	sem := make(chan struct{}, c.prefetchLimit)
	var wg sync.WaitGroup

	for i, slug := range slugs {
		wg.Add(1)
		go func(i int, slug string) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				return
			}
			if s, err := c.Spell(ctx, slug); err == nil {
				out[i] = s
			}
		}(i, slug)
	}
	wg.Wait()

	spells := make([]*Spell, 0, len(out))
	for _, s := range out {
		if s != nil {
			spells = append(spells, s)
		}
	}
	return spells
}

// Slugify turns player input such as "Young Black Dragon" into the slug the
// API expects, "young-black-dragon".
func Slugify(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	dash := false
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			dash = false
		case r == ' ' || r == '-' || r == '_' || r == '\'':
			if !dash && b.Len() > 0 {
				b.WriteByte('-')
				dash = true
			}
		}
	}
	return strings.Trim(b.String(), "-")
}
