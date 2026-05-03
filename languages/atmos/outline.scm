; Terraform components
(block_mapping_pair
  key: (flow_node (plain_scalar (string_scalar) @_comp_type))
  (#eq? @_comp_type "terraform")
  value: (block_node (block_mapping
    (block_mapping_pair
      key: (flow_node (plain_scalar (string_scalar) @name))
      ) @item))) @context

; Helmfile components
(block_mapping_pair
  key: (flow_node (plain_scalar (string_scalar) @_comp_type2))
  (#eq? @_comp_type2 "helmfile")
  value: (block_node (block_mapping
    (block_mapping_pair
      key: (flow_node (plain_scalar (string_scalar) @name))
      ) @item))) @context

; Import paths as context
(block_mapping_pair
  key: (flow_node (plain_scalar (string_scalar) @_import))
  (#eq? @_import "import")
  value: (block_node (block_sequence
    (block_sequence_item (flow_node (plain_scalar (string_scalar) @name))))))
