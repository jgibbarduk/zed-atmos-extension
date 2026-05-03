; Top-level Atmos keywords
(block_mapping_pair
  key: (flow_node (plain_scalar (string_scalar) @keyword))
  (#match? @keyword "^(import|vars|settings|env|components|metadata|terraform|helmfile|provider|backend|overrides|namespace|tenant|environment|stage)$"))

; Component names under components.terraform.* or components.helmfile.*
; Assumes the value uses block_mapping syntax (i.e., indented sub-keys).
(block_mapping_pair
  key: (flow_node (plain_scalar (string_scalar) @_comp_type))
  (#match? @_comp_type "^(terraform|helmfile)$")
  value: (block_node (block_mapping
    (block_mapping_pair
      key: (flow_node (plain_scalar (string_scalar) @function))))))

; metadata.component value (Terraform module reference)
(block_mapping_pair
  key: (flow_node (plain_scalar (string_scalar) @_meta_key))
  (#eq? @_meta_key "component")
  value: (block_node (flow_node (plain_scalar (string_scalar) @type))))

; Import path values
(block_mapping_pair
  key: (flow_node (plain_scalar (string_scalar) @_import_key))
  (#eq? @_import_key "import")
  value: (block_node (block_sequence
    (block_sequence_item (flow_node (plain_scalar (string_scalar) @string.special))))))

; Metadata inherits - highlight the key
(block_mapping_pair
  key: (flow_node (plain_scalar (string_scalar) @keyword))
  (#eq? @keyword "inherits"))

; String values
(string_scalar) @string

; Numbers
(float_scalar) @number
(integer_scalar) @number

; Boolean values
(boolean_scalar) @boolean

; Null
(null_scalar) @constant

; Comments
(comment) @comment

; Block scalars (multi-line strings)
(block_scalar) @string
