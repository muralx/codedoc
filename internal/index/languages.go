package index

// The extractors register themselves from init functions; importing them
// here makes every language available wherever the pipeline is linked.
import (
	_ "github.com/muralx/codedoc/internal/extract/doc"    // Markdown, reStructuredText, AsciiDoc and text documents
	_ "github.com/muralx/codedoc/internal/extract/golang" // Go extractor
	_ "github.com/muralx/codedoc/internal/extract/java"   // Java extractor
	_ "github.com/muralx/codedoc/internal/extract/python" // Python extractor
	_ "github.com/muralx/codedoc/internal/extract/ts"     // TypeScript and JavaScript extractor
)
