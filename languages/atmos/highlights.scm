; Atmos YAML Syntax Highlighting
; Uses tree-sitter-yaml grammar named node types

; === Comments ===
(comment) @comment

; === Scalar types ===
(integer_scalar) @number
(float_scalar) @number
(boolean_scalar) @boolean
(null_scalar) @constant
(block_scalar) @string
(double_quote_scalar) @string
(single_quote_scalar) @string
(string_scalar) @string

; === Top-level Atmos keywords ===
(block_mapping_pair
  key: (flow_node
    (plain_scalar
      (string_scalar) @keyword))
  (#match? @keyword "^(import|vars|locals|settings|env|components|metadata|terraform|helmfile|providers|overrides|backend|remote_state|workflows)$"))

; === Component names under terraform/helmfile ===
(block_mapping_pair
  key: (flow_node (plain_scalar (string_scalar) @_type))
  (#match? @_type "^(terraform|helmfile)$")
  value: (block_node (block_mapping
    (block_mapping_pair
      key: (flow_node (plain_scalar (string_scalar) @function))))))

; === metadata.component value (Terraform module reference) ===
(block_mapping_pair
  key: (flow_node (plain_scalar (string_scalar) @_meta_key))
  (#eq? @_meta_key "component")
  value: (flow_node (plain_scalar (string_scalar) @type)))

; === Tags and anchors ===
(tag) @tag
(anchor) @label
(alias) @label

; === Brackets / flow punctuation ===
("[" @punctuation.bracket)
("]" @punctuation.bracket)
("{" @punctuation.bracket)
("}" @punctuation.bracket)

; === Parse errors ===
(ERROR) @error
