package overlaysubs

import _ "embed"

// ModelsJSON embeds the repository's models.json registry verbatim.
//
//go:embed models.json
var ModelsJSON []byte
