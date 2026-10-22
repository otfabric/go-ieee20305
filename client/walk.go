// SPDX-License-Identifier: MIT

package client

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"reflect"
	"strconv"
	"time"

	ieee20305 "github.com/otfabric/go-ieee20305"
)

// Head checks that the resource at href exists, without its document.
func (c *Client) Head(ctx context.Context, href string) error {
	_, _, err := c.do(ctx, http.MethodHead, href, nil, nil)
	return err
}

// FetchList retrieves a page of the list resource at href and returns its
// document, of whatever list type the server sent.
func (c *Client) FetchList(ctx context.Context, href string, opts ListOptions) (ieee20305.Lister, error) {
	q := url.Values{}
	q.Set("s", strconv.FormatUint(uint64(opts.Start), 10))
	if opts.Limit > 0 {
		q.Set("l", strconv.FormatUint(uint64(opts.Limit), 10))
	}
	if opts.SFDI != 0 {
		q.Set("sFDI", strconv.FormatUint(opts.SFDI, 10))
	}
	data, err := c.body(ctx, href, q)
	if err != nil {
		return nil, err
	}
	v, err := c.codec.Decode(data)
	if err != nil {
		return nil, err
	}
	l, ok := v.(ieee20305.Lister)
	if !ok {
		return nil, fmt.Errorf("%w: %s is a %T, not a list", ieee20305.ErrProtocol, href, v)
	}
	return l, nil
}

// Iterate calls fn with every item of the list resource at href, a page of
// pageSize items at a time (all the server gives when zero), until the list
// ends or fn returns an error, which Iterate returns. A list longer than
// Config.MaxListItems fails with ErrProtocol.
func (c *Client) Iterate(ctx context.Context, href string, pageSize uint32, fn func(item any) error) error {
	return c.iterate(ctx, href, 0, pageSize, fn)
}

// iterate is Iterate from the item start.
func (c *Client) iterate(ctx context.Context, href string, start, pageSize uint32, fn func(item any) error) error {
	limit := pageSize
	if limit == 0 {
		limit = 255
	}
	for {
		l, err := c.FetchList(ctx, href, ListOptions{Start: start, Limit: limit})
		if err != nil {
			return err
		}
		items := l.ListItems()
		for _, it := range items {
			if err := fn(it); err != nil {
				return err
			}
		}
		start += uint32(len(items))
		if start > c.maxItems {
			return fmt.Errorf("%w: %s has more than %d items", ieee20305.ErrProtocol, href, c.maxItems)
		}
		if len(items) == 0 || start >= listAll(l) {
			return nil
		}
	}
}

// listAll returns the all attribute of a list (of List or
// SubscribableList, which every list type embeds).
func listAll(l ieee20305.Lister) uint32 {
	v := reflect.ValueOf(l)
	if v.Kind() != reflect.Pointer || v.IsNil() {
		return 0
	}
	if f := v.Elem().FieldByName("All"); f.IsValid() && f.CanUint() {
		return uint32(f.Uint())
	}
	return 0
}

// Step is a resource Walk visits.
type Step struct {
	// Href is the href of the resource.
	Href string
	// Link is the link Walk followed to it; the zero LinkRef for the root.
	Link ieee20305.LinkRef
	// Doc is its document, such as *ieee20305.EndDevice; for a list, the
	// list with all of its items.
	Doc any
	// Depth is the number of links Walk followed from the root.
	Depth int
}

// WalkOptions limit what Walk visits.
type WalkOptions struct {
	// MaxDepth is the number of links Walk follows from the root; 8 when
	// zero.
	MaxDepth int
	// Follow, when set, decides which links Walk follows.
	Follow func(l ieee20305.LinkRef) bool
	// PageSize is the page size of lists; see Iterate.
	PageSize uint32
}

// Walk visits the resource at root and the resources its links lead to,
// breadth first, each once: lists with their items, and the links of the
// items. A link to a resource the server does not have (404) is skipped.
// visit's error stops the walk and is returned.
func (c *Client) Walk(ctx context.Context, root string, opts WalkOptions, visit func(Step) error) error {
	if opts.MaxDepth == 0 {
		opts.MaxDepth = 8
	}
	type todo struct {
		href  string
		link  ieee20305.LinkRef
		depth int
	}
	seen := map[string]bool{root: true}
	queue := []todo{{href: root}}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		doc, err := c.Fetch(ctx, cur.href)
		var se *ieee20305.StatusError
		if errors.As(err, &se) && se.StatusCode == http.StatusNotFound && cur.depth > 0 {
			continue
		}
		if err != nil {
			return err
		}
		docs := []any{doc}
		if l, ok := doc.(ieee20305.Lister); ok {
			// The items of the first page, then the rest of the list.
			items := l.ListItems()
			if all := listAll(l); uint32(len(items)) < all && len(items) > 0 {
				if err := c.iterate(ctx, cur.href, uint32(len(items)), opts.PageSize, func(it any) error {
					items = append(items, it)
					return nil
				}); err != nil {
					return err
				}
			} else if len(items) == 0 && all > 0 {
				items = nil
				if err := c.Iterate(ctx, cur.href, opts.PageSize, func(it any) error {
					items = append(items, it)
					return nil
				}); err != nil {
					return err
				}
			}
			if err := l.SetListItems(items); err != nil {
				return err
			}
			l.SetListCounts(uint32(len(items)), uint32(len(items)))
			docs = append(docs, items...)
		}
		if err := visit(Step{Href: cur.href, Link: cur.link, Doc: doc, Depth: cur.depth}); err != nil {
			return err
		}
		if cur.depth >= opts.MaxDepth {
			continue
		}
		for _, d := range docs {
			for _, l := range ieee20305.Links(d) {
				if l.Href == "" || seen[l.Href] || (opts.Follow != nil && !opts.Follow(l)) {
					continue
				}
				if _, err := c.Resolve(l.Href); err != nil {
					continue // another server: not followed
				}
				seen[l.Href] = true
				queue = append(queue, todo{href: l.Href, link: l, depth: cur.depth + 1})
			}
		}
	}
	return nil
}

// TimeSync is the server's time, as of a Time request.
type TimeSync struct {
	// Time is the server's Time resource.
	Time *ieee20305.Time
	// Offset is the server's time minus the local time, at the request.
	Offset time.Duration
}

// Now returns the local time corrected to the server's.
func (ts TimeSync) Now() time.Time { return time.Now().Add(ts.Offset) }

// Time retrieves the server's Time resource, from the TimeLink of its
// DeviceCapability, and the offset of the local clock.
func (c *Client) Time(ctx context.Context) (TimeSync, error) {
	dc, err := c.DeviceCapability(ctx)
	if err != nil {
		return TimeSync{}, err
	}
	if dc.TimeLink == nil {
		return TimeSync{}, fmt.Errorf("%w: the DeviceCapability has no TimeLink", ieee20305.ErrProtocol)
	}
	before := time.Now()
	var tm ieee20305.Time
	if err := c.Get(ctx, dc.TimeLink.Href, &tm); err != nil {
		return TimeSync{}, err
	}
	mid := before.Add(time.Since(before) / 2)
	return TimeSync{Time: &tm, Offset: time.Unix(int64(tm.CurrentTime), 0).Sub(mid.Truncate(time.Second))}, nil
}
