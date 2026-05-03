; Inject Go template grammar into YAML string scalars.
; Atmos uses Go template syntax ({{ .vars }}, {{ range }}, {{ if }}, etc.)
; within YAML string values.

(
  (string_scalar) @injection.content
  (#set! injection.language "gotmpl")
  (#set! injection.combined)
)

(
  (double_quote_scalar) @injection.content
  (#set! injection.language "gotmpl")
  (#set! injection.combined)
)

(
  (single_quote_scalar) @injection.content
  (#set! injection.language "gotmpl")
  (#set! injection.combined)
)

(
  (block_scalar) @injection.content
  (#set! injection.language "gotmpl")
  (#set! injection.combined)
)
