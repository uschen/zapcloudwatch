// Copyright 2024 Chen Liang. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package zapcloudwatch

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs/types"

	bundler "github.com/uschen/gbundler"
)

const (
	// defaultErrorCapacity is the capacity of the channel used to deliver
	// errors to the OnError function.
	defaultErrorCapacity = 10

	// defaultWriteTimeout is the timeout for the underlying write API calls. As
	// write API calls are not idempotent, they are not retried on timeout. This
	// timeout is to allow clients to degrade gracefully if underlying logging
	// service is temporarily impaired for some reason.
	defaultWriteTimeout = 10 * time.Minute
)

var (
	// ErrOverflow signals that the number of buffered entries for a Logger
	// exceeds its BufferLimit.
	ErrOverflow = bundler.ErrOverflow

	// ErrOversizedEntry signals that an entry's size exceeds the maximum number of
	// bytes that will be sent in a single call to the logging service.
	ErrOversizedEntry = bundler.ErrOversizedItem
)

type client struct {
	logGroupName  string
	logStreamName string

	cwc *cloudwatchlogs.Client

	errc chan error // should be buffered to minimize dropped errors

	b *bundler.Bundler[types.InputLogEvent]

	mu      sync.Mutex
	nErrs   int   // number of errors we saw
	lastErr error // last error we saw

	closed bool

	// OnError is called when an error occurs in a call to Log or Flush. The
	// error may be due to an invalid Entry, an overflow because BufferLimit
	// was reached (in which case the error will be ErrOverflow) or an error
	// communicating with the logging service. OnError is called with errors
	// from all Loggers. It is never called concurrently. OnError is expected
	// to return quickly; if errors occur while OnError is running, some may
	// not be reported. The default behavior is to call log.Printf.
	//
	// This field should be set only once, before any method of Client is called.
	OnError func(err error)
}

func (c *client) writeLogEvents(events []types.InputLogEvent) {
	ctx, cancel := context.WithTimeout(context.Background(), defaultWriteTimeout)
	defer cancel()
	_, err := c.cwc.PutLogEvents(ctx, &cloudwatchlogs.PutLogEventsInput{
		LogEvents:     events,
		LogGroupName:  aws.String(c.logGroupName),
		LogStreamName: aws.String(c.logStreamName),
	})
	if err != nil {
		c.error(err)
	}
}

// error puts the error on the client's error channel
// without blocking, and records summary error info.
func (c *client) error(err error) {
	select {
	case c.errc <- err:
	default:
	}
	c.mu.Lock()
	c.lastErr = err
	c.nErrs++
	c.mu.Unlock()
}

func (c *client) extractErrorInfo() error {
	var err error
	c.mu.Lock()
	if c.lastErr != nil {
		err = fmt.Errorf("saw %d errors; last: %w", c.nErrs, c.lastErr)
		c.nErrs = 0
		c.lastErr = nil
	}
	c.mu.Unlock()
	return err
}
