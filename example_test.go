// Copyright 2024 Chen Liang. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package zapcloudwatch_test

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	"github.com/uschen/zapcloudwatch"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

func Example_custom_client() {
	logGroup := "YOUR-LOG-GROUP"
	logStream := "YOUR-LOG-STREAM"
	awsConfigProfile := "YOUR-PROFILE-NAME"

	cfg, err := config.LoadDefaultConfig(context.TODO(), config.WithSharedConfigProfile(awsConfigProfile))
	if err != nil {
		panic("configuration error: " + err.Error())
	}
	cl := cloudwatchlogs.NewFromConfig(cfg)

	core, err := zapcloudwatch.NewCore(
		zapcore.NewJSONEncoder(zap.NewProductionEncoderConfig()),
		logGroup,
		logStream,
		zap.InfoLevel,
		zapcloudwatch.WithCloudWatchClient(cl),
		zapcloudwatch.WithOnError(func(err error) {
			fmt.Printf("got error: %s", err)
		}),
	)
	if err != nil {
		panic(err)
	}

	logger := zap.New(core)
	logger.Debug("this is a debug message which will not be logged due to INFO zapcore.LevelEnabler passed above")
	logger.Info("test message", zap.String("string_field", "string value"), zap.Int64("int64_field", 123))
	logger.Sync() // flush the logs to CloudWatch Log

	l2 := logger.With(zap.String("sub_logger", "logger2"))
	l2.Info("sub logger message", zap.Any("struct_value", struct {
		A string
		B string
	}{
		A: "value a",
		B: "value b",
	}))

	// flush
	if err := core.Close(); err != nil {
		panic(err)
	}
}
