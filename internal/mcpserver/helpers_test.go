package mcpserver_test

import "github.com/margarit/printpipe/internal/pipeline"

func pipelineFilter(session string) pipeline.RunFilter { return pipeline.RunFilter{Session: session} }
