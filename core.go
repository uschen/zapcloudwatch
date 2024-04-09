// Copyright 2024 Chen Liang. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package zapcloudwatch

import (
	"context"
	"fmt"
	"log"
	"time"

	bundler "github.com/uschen/gbundler"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs/types"
	"go.uber.org/zap/zapcore"
)

// Core zapcore.Core sinks to Amazon CloudWatch Log
type Core struct {
	zapcore.LevelEnabler

	c *client

	enc zapcore.Encoder
}

type params struct {
	createLogGroup  bool
	createLogStream bool
}

var (
	_ zapcore.Core         = (*Core)(nil)
	_ zapcore.LevelEnabler = (*Core)(nil)
)

// OptionFunc -
type OptionFunc func(*Core, *params) error

// WithCloudWatchClient passes existing cloudwatchlogs.Client
func WithCloudWatchClient(client *cloudwatchlogs.Client) OptionFunc {
	return func(cc *Core, p *params) error {
		cc.c.cwc = client
		return nil
	}
}

// WithCreateLogGroup whether to create log group if it doesn't exist.
// Default is false.
func WithCreateLogGroup(create bool) OptionFunc {
	return func(cc *Core, p *params) error {
		p.createLogGroup = create
		return nil
	}
}

// WithCreateLogStream whether to create log stream if it doesn't exist.
// Default is false.
func WithCreateLogStream(create bool) OptionFunc {
	return func(cc *Core, p *params) error {
		p.createLogStream = create
		return nil
	}
}

// WithBundlerDelayThreshold Starting from the time that the first message is added to a bundle, once
// this delay has passed, handle the bundle. The default is
// bundler.DefaultDelayThreshold
func WithBundlerDelayThreshold(threshold time.Duration) OptionFunc {
	return func(cc *Core, p *params) error {
		cc.c.b.DelayThreshold = threshold
		return nil
	}
}

// WithBundlerBundleCountThreshold Once a bundle has this many items, handle the bundle. Since only one
// item at a time is added to a bundle, no bundle will exceed this
// threshold, so it also serves as a limit. The default is
// bundler.DefaultBundleCountThreshold.
func WithBundlerBundleCountThreshold(threshold int) OptionFunc {
	return func(cc *Core, p *params) error {
		cc.c.b.BundleCountThreshold = threshold
		return nil
	}
}

// WithBundlerBundleByteThreshold Once the number of bytes in current bundle reaches this
// threshold, handle the bundle. The default is
// bundler.DefaultBundleByteThreshold. This triggers handling, but does not cap
// the total size of a bundle.
func WithBundlerBundleByteThreshold(threshold int) OptionFunc {
	return func(cc *Core, p *params) error {
		cc.c.b.BundleByteThreshold = threshold
		return nil
	}
}

// WithBundlerBundleByteLimit The maximum size of a bundle, in bytes. Zero means unlimited.
func WithBundlerBundleByteLimit(limit int) OptionFunc {
	return func(cc *Core, p *params) error {
		cc.c.b.BundleByteLimit = limit
		return nil
	}
}

// WithBundlerBufferedByteLimit The maximum number of bytes that the Bundler will
// keep in memory before returning ErrOverflow. The default is
// bundler.DefaultBufferedByteLimit
func WithBundlerBufferedByteLimit(limit int) OptionFunc {
	return func(cc *Core, p *params) error {
		cc.c.b.BufferedByteLimit = limit
		return nil
	}
}

// WithBundlerHandlerLimit The maximum number of handler invocations that
// can be running at once. The default is 1.
func WithBundlerHandlerLimit(limit int) OptionFunc {
	return func(cc *Core, p *params) error {
		cc.c.b.HandlerLimit = limit
		return nil
	}
}

// WithOnError OnError is called when an error occurs in a call to Log or Flush.
func WithOnError(onError func(error)) OptionFunc {
	return func(cc *Core, p *params) error {
		cc.c.OnError = onError
		return nil
	}
}

func NewCore(enc zapcore.Encoder, logGroupName, logStreamName string, enab zapcore.LevelEnabler, options ...OptionFunc) (*Core, error) {
	c := &client{
		logGroupName:  logGroupName,
		logStreamName: logStreamName,

		errc: make(chan error, defaultErrorCapacity), // create a small buffer for errors

		OnError: func(e error) { log.Printf("logging client: %v", e) },
	}

	c.b = bundler.NewBundler(c.writeLogEvents)

	cc := &Core{
		LevelEnabler: enab,
		enc:          enc,

		c: c,
	}

	p := &params{}
	for _, optionFn := range options {
		if err := optionFn(cc, p); err != nil {
			return nil, err
		}
	}

	if cc.c.cwc == nil {
		// initialize default cloudwatchlogs client
		cfg, err := config.LoadDefaultConfig(context.TODO())
		if err != nil {
			return nil, err
		}
		cc.c.cwc = cloudwatchlogs.NewFromConfig(cfg)
	}

	// check log group
	lgRes, err := cc.c.cwc.DescribeLogGroups(context.TODO(), &cloudwatchlogs.DescribeLogGroupsInput{
		LogGroupNamePrefix: aws.String(cc.c.logGroupName),
		Limit:              aws.Int32(1),
	})
	if err != nil {
		return nil, err
	}

	if len(lgRes.LogGroups) < 1 {
		// we need to create this log group
		// check create log group and stream
		if p.createLogGroup {
			_, err := cc.c.cwc.CreateLogGroup(context.TODO(), &cloudwatchlogs.CreateLogGroupInput{LogGroupName: aws.String(cc.c.logGroupName)})
			if err != nil {
				return nil, err
			}
		} else {
			return nil, fmt.Errorf("log group doesn't exist: '%s'", logGroupName)
		}
	}

	// check log stream

	lsRes, err := cc.c.cwc.DescribeLogStreams(context.TODO(), &cloudwatchlogs.DescribeLogStreamsInput{
		LogGroupName:        aws.String(logGroupName), // Required
		LogStreamNamePrefix: aws.String(logStreamName),
	})
	if err != nil {
		return nil, err
	}

	if len(lsRes.LogStreams) < 1 {
		if p.createLogStream {
			_, err = cc.c.cwc.CreateLogStream(context.TODO(), &cloudwatchlogs.CreateLogStreamInput{
				LogGroupName:  aws.String(logGroupName),
				LogStreamName: aws.String(logStreamName),
			})
			if err != nil {
				return nil, err
			}
		} else {
			return nil, fmt.Errorf("log stream doesn't exist: '%s'", logStreamName)
		}
	}

	// Call the user's function synchronously, to make life easier for them.
	go func() {
		for err := range c.errc {
			// This reference to OnError is memory-safe if the user sets OnError before
			// calling any client methods. The reference happens before the first read from
			// client.errc, which happens before the first write to client.errc, which
			// happens before any call, which happens before the user sets OnError.
			if fn := c.OnError; fn != nil {
				fn(err)
			} else {
				log.Printf("logging: %v", err)
			}
		}
	}()
	return cc, nil
}

// Close flushes and return flush error.
func (cc *Core) Close() error {
	if cc.c.closed {
		return nil
	}
	cc.c.b.Flush()
	// Now there can be no more errors.
	close(cc.c.errc) // terminate error goroutine
	// Prefer errors arising from logging to the error returned from Close.
	err := cc.c.extractErrorInfo()
	cc.c.closed = true
	return err
}

func (cc *Core) Level() zapcore.Level {
	return zapcore.LevelOf(cc.LevelEnabler)
}

func (cc *Core) With(fields []zapcore.Field) zapcore.Core {
	clone := cc.clone()
	addFields(clone.enc, fields)
	return clone
}

func (cc *Core) Check(ent zapcore.Entry, ce *zapcore.CheckedEntry) *zapcore.CheckedEntry {
	if cc.Enabled(ent.Level) {
		return ce.AddCore(ent, cc)
	}
	return ce
}

func (cc *Core) Write(ent zapcore.Entry, fields []zapcore.Field) error {
	buf, err := cc.enc.EncodeEntry(ent, fields)
	if err != nil {
		return err
	}

	msg := buf.String()
	// calculate size:
	// The maximum batch size is 1,048,576 bytes. This size is calculated as the
	// sum of all event messages in UTF-8, plus 26 bytes for each log event.
	size := len(msg) + 26
	if err := cc.c.b.Add(types.InputLogEvent{
		Message:   aws.String(buf.String()),
		Timestamp: aws.Int64(ent.Time.UnixMilli()),
	}, size); err != nil {
		return err
	}

	if ent.Level > zapcore.ErrorLevel {
		// Since we may be crashing the program, sync the output.
		// Ignore Sync errors, pending a clean solution to issue #370.
		_ = cc.Sync()
	}
	return nil
}
func (cc *Core) Sync() error {
	cc.c.b.Flush()
	return cc.c.extractErrorInfo()
}

func (cc *Core) clone() *Core {
	return &Core{
		LevelEnabler: cc.LevelEnabler,
		enc:          cc.enc.Clone(),

		c: cc.c,
	}
}

func addFields(enc zapcore.ObjectEncoder, fields []zapcore.Field) {
	for i := range fields {
		fields[i].AddTo(enc)
	}
}
