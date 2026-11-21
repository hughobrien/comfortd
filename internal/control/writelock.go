// SPDX-License-Identifier: GPL-3.0-or-later

package control

import (
	"context"
	"sync"
)

// refreshTimeout bounds the poll that follows a write. A hold change polls
// every unit, so it gets more than one send.
const refreshTimeout = 2 * sendTimeout

// lockWrites serialises schedule, hold and limit writes for one unit, so a
// repeated Save runs after the first and sees what it left. It returns the
// context the write must use: detached from the browser request, because the
// UI cancels a request on a second click or a closed tab and a multi-step
// write (add events, then delete the old ones) must not stop halfway. Each
// cloud call still has its own timeout. If the request is cancelled while
// waiting for the lock nothing has started, so it gives up with ctx's error.
func (c *Controller) lockWrites(ctx context.Context, slug string) (unlock func(), wctx context.Context, err error) {
	m, _ := c.writeMu.LoadOrStore(slug, &sync.Mutex{})
	mu := m.(*sync.Mutex)
	mu.Lock()
	if err := ctx.Err(); err != nil {
		mu.Unlock()
		return nil, ctx, err
	}
	return mu.Unlock, context.WithoutCancel(ctx), nil
}

// refresh runs a poll hook (if any) on its own bounded context, so a
// cancelled request cannot make the poller record "context canceled" as an
// endpoint error.
func (c *Controller) refresh(ctx context.Context, hook func(context.Context)) {
	if hook == nil {
		return
	}
	rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), refreshTimeout)
	defer cancel()
	hook(rctx)
}
