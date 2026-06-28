package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/go-app-blazar/blazar/blazar"
	"github.com/go-app-blazar/blazar/blazarapp"
	"github.com/go-app-blazar/router"
	"github.com/joho/godotenv"
	"github.com/lmittmann/tint"
	"github.com/mattn/go-isatty"
	"github.com/maxence-charriere/go-app/v11/pkg/app"
	"github.com/tekkamanendless/csd-tax-parcel-analysis/demo"
	"github.com/tekkamanendless/csd-tax-parcel-analysis/mainplugin"
	"github.com/tekkamanendless/csd-tax-parcel-analysis/simplechart"
)

// The main function is the entry point where the app is configured and started.
// It is executed in 2 different environments: A client (the web browser) and a
// server.
func main() {
	ctx := context.Background()

	godotenv.Load(".env")

	{
		logLevel := "info"
		if value := os.Getenv("LOG_LEVEL"); value != "" {
			logLevel = value
		}
		slogConfig := slog.HandlerOptions{
			Level:     slog.LevelInfo,
			AddSource: true,
			ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
				if a.Key == slog.SourceKey {
					source, _ := a.Value.Any().(*slog.Source)
					if source != nil {
						source.File = filepath.Base(source.File)
					}
				}
				return a
			},
		}
		switch strings.ToLower(logLevel) {
		case "debug":
			slogConfig.Level = slog.LevelDebug
		case "info":
			slogConfig.Level = slog.LevelInfo
		case "warn":
			slogConfig.Level = slog.LevelWarn
		case "error":
			slogConfig.Level = slog.LevelError
		}
		var handler slog.Handler
		if app.IsServer {
			//handler = slog.NewTextHandler(os.Stderr, &slogConfig)
			w := os.Stderr
			handler =
				tint.NewHandler(w, &tint.Options{
					NoColor:     !isatty.IsTerminal(w.Fd()),
					AddSource:   slogConfig.AddSource,
					Level:       slogConfig.Level,
					ReplaceAttr: slogConfig.ReplaceAttr,
				})
		} else {
			handler = &JavascriptConsoleLogger{
				Level: slogConfig.Level,
			}
		}
		slog.SetDefault(slog.New(slog.NewMultiHandler(
			handler,
		)))

		slog.InfoContext(ctx, "Logging level", "level", slogConfig.Level)
	}
	if slog.Default().Enabled(ctx, slog.LevelDebug) {
		slog.InfoContext(ctx, "Logging level includes debug")
	}
	if slog.Default().Enabled(ctx, slog.LevelInfo) {
		slog.InfoContext(ctx, "Logging level includes info")
	}
	if slog.Default().Enabled(ctx, slog.LevelWarn) {
		slog.InfoContext(ctx, "Logging level includes warn")
	}
	if slog.Default().Enabled(ctx, slog.LevelError) {
		slog.InfoContext(ctx, "Logging level includes error")
	}

	disableServiceWorker := true
	if value := os.Getenv("DISABLE_SERVICE_WORKER"); value != "" {
		v, err := strconv.ParseBool(value)
		if err != nil {
			slog.ErrorContext(ctx, "Could not parse DISABLE_SERVICE_WORKER", "err", err)
		} else {
			disableServiceWorker = v
		}
	}

	generateStaticFiles := false
	if value := os.Getenv("GENERATE_STATIC_FILES"); value != "" {
		v, err := strconv.ParseBool(value)
		if err != nil {
			slog.ErrorContext(ctx, "Could not parse GENERATE_STATIC_FILES", "err", err)
		} else {
			generateStaticFiles = v
		}
	}

	router.Register(ctx,
		router.Route{
			Path: "/",
			Component: func() app.Composer {
				return blazar.MainLayout().
					HeadlineText("New Castle County School Tax Calculator").
					Drawer(nil)
			},
			Children: []router.Route{
				{
					Path: "/",
					Component: func() app.Composer {
						return &demo.IndexPage{}
					},
				},
			},
		},
	)

	// Once the routes set up, the next thing to do is to either launch the app
	// or the server that serves the app.
	//
	// When executed on the client-side, the RunWhenOnBrowser() function
	// launches the app,  starting a loop that listens for app events and
	// executes client instructions. Since it is a blocking call, the code below
	// it will never be executed.
	//
	// When executed on the server-side, RunWhenOnBrowser() does nothing, which
	// lets room for server implementation without the need for precompiling
	// instructions.
	app.RunWhenOnBrowser()

	blazarApp := blazarapp.NewApp(blazarapp.Config{
		Name:        "New Castle County School Tax Calculator",
		Description: "New Castle County School Tax Calculator",
		Title:       "New Castle County School Tax Calculator",
	})
	blazarApp.AddPlugin(blazarapp.DefaultPlugins()...)
	blazarApp.AddPlugin(simplechart.NewPlugin(simplechart.Config{
		Location: "/web/simplechart/",
	}))
	blazarApp.AddPlugin(mainplugin.NewPlugin(mainplugin.Config{
		Location: "/web/main/",
	}))

	slog.InfoContext(ctx, "Disable service worker?", "disableServiceWorker", disableServiceWorker)
	if disableServiceWorker {
		blazarApp.DisableServiceWorker()
	}

	wrapper := http.NewServeMux()
	wrapper.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		slog.InfoContext(r.Context(), "[in] Request", "method", r.Method, "url", r.URL.String())
		wrappedResponseWriter := &ResponseWriter{
			writer: w,
		}
		wrappedResponseWriter.Info.StatusCode = http.StatusOK
		blazarApp.ServeHTTP(wrappedResponseWriter, r)
		slog.InfoContext(r.Context(), "[out] Request", "method", r.Method, "url", r.URL.String(), "code", wrappedResponseWriter.Info.StatusCode)
	})

	if generateStaticFiles {
		err := blazarApp.GenerateStaticFiles()
		if err != nil {
			slog.ErrorContext(ctx, "Could not generate static files", "err", err)
			os.Exit(1)
		}
		slog.InfoContext(ctx, "Static files generated successfully.")
		os.Exit(0)
	}

	port := os.Getenv("PORT")
	if port == "" {
		port = "8001"
	}
	if err := http.ListenAndServe(":"+port, wrapper); err != nil {
		slog.ErrorContext(ctx, "Could not start server", "err", err)
		os.Exit(1)
	}
}

type ResponseWriter struct {
	writer http.ResponseWriter

	Info struct {
		StatusCode   int
		BytesWritten uint64
	}
}

var _ http.ResponseWriter = (*ResponseWriter)(nil)

func (w *ResponseWriter) Header() http.Header {
	return w.writer.Header()
}

func (w *ResponseWriter) Write(contents []byte) (int, error) {
	count, err := w.writer.Write(contents)
	if err != nil {
		return count, err
	}
	w.Info.BytesWritten += uint64(count)
	return count, err
}

func (w *ResponseWriter) WriteHeader(statusCode int) {
	w.Info.StatusCode = statusCode
	w.writer.WriteHeader(statusCode)
}

type JavascriptConsoleLogger struct {
	Level slog.Leveler
	attrs []slog.Attr
	group string
}

var _ slog.Handler = (*JavascriptConsoleLogger)(nil)

func (h *JavascriptConsoleLogger) Enabled(ctx context.Context, level slog.Level) bool {
	return level >= h.Level.Level()
}
func (h *JavascriptConsoleLogger) Handle(ctx context.Context, record slog.Record) error {
	if app.IsServer {
		return nil
	}
	var variables []any
	variables = append(variables, record.Message)
	record.Attrs(func(a slog.Attr) bool {
		variables = append(variables, a.Key, "=", a.Value)
		return true
	})
	switch record.Level {
	case slog.LevelDebug:
		app.Log(variables...) // TODO: Can we do console.debug?
	case slog.LevelInfo:
		app.Log(variables...)
	case slog.LevelWarn:
		app.Log(variables...) // TODO: Can we do console.warn?
	case slog.LevelError:
		app.Log(variables...) // TODO: Can we do console.error?
	default:
		app.Log(variables...)
	}
	return nil
}
func (h *JavascriptConsoleLogger) WithAttrs(attrs []slog.Attr) slog.Handler {
	newHandler := JavascriptConsoleLogger{
		group: h.group,
	}
	newHandler.attrs = append(newHandler.attrs, h.attrs...)
	newHandler.attrs = append(newHandler.attrs, attrs...)

	return &newHandler
}
func (h *JavascriptConsoleLogger) WithGroup(name string) slog.Handler {
	newHandler := JavascriptConsoleLogger{
		group: name,
	}
	newHandler.attrs = append(newHandler.attrs, h.attrs...)

	return &newHandler
}
