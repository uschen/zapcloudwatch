# zapcloudwatch

<div align="center">

[![GoDoc][doc-img]][doc]

</div>

## Installation

```sh
go get -u github.com/uschen/zapstackdriver
```

## Quick Start

```go
logGroup := "YOUR-LOG-GROUP"
logStream := "YOUR-LOG-STREAM"

core, err := zapcloudwatch.NewCore(
  zapcore.NewJSONEncoder(zap.NewProductionEncoderConfig()),
  logGroup,
  logStream,
  zap.InfoLevel,
  zapcloudwatch.WithOnError(func(err error) {
    fmt.Printf("got error: %s", err)
  }),
)
if err != nil {
  panic(err)
}

logger := zap.New(core)
logger.Info("test message", zap.String("string_field", "string value"), zap.Int64("int64_field", 123))
logger.Sync() // flushes the logs to CloudWatch Log

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
```

## Contributing

<hr>

Released under the [BSD 3-Clause License](LICENSE).

[doc-img]: https://pkg.go.dev/badge/github.com/uschen/zapcloudwatch
[doc]: https://pkg.go.dev/github.com/uschen/zapcloudwatch
