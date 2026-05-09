use zed_extension_api::{self as zed, Command, Extension, LanguageServerId, Result, Worktree};

struct AtmosExtension {
    cached_binary_path: Option<String>,
}

impl AtmosExtension {
    /// Resolve the binary path using the provided lookup function,
    /// caching the result so subsequent calls are zero-cost.
    fn resolve_binary_path<F>(&mut self, lookup: F) -> Result<String>
    where
        F: FnOnce() -> Option<String>,
    {
        if let Some(path) = &self.cached_binary_path {
            return Ok(path.clone());
        }

        let path = lookup().ok_or_else(|| {
            "atmos-lsp-bridge not found in PATH. Install it from https://github.com/jgibbarduk/zed-atmos-extension/releases"
                .to_string()
        })?;

        self.cached_binary_path = Some(path.clone());
        Ok(path)
    }
}

impl Extension for AtmosExtension {
    fn new() -> Self {
        Self {
            cached_binary_path: None,
        }
    }

    fn language_server_command(
        &mut self,
        // Unused: this extension only registers a single language server, so we do not
        // need to dispatch on the server ID.
        _language_server_id: &LanguageServerId,
        worktree: &Worktree,
    ) -> Result<Command> {
        let binary_path = self.resolve_binary_path(|| worktree.which("atmos-lsp-bridge"))?;

        Ok(Command {
            command: binary_path,
            args: vec![],
            env: vec![],
        })
    }

    fn language_server_initialization_options(
        &mut self,
        _language_server_id: &LanguageServerId,
        _worktree: &Worktree,
    ) -> Result<Option<serde_json::Value>> {
        Ok(Some(serde_json::json!({
            "stacks_path": "stacks",
            "diagnostics_enabled": true
        })))
    }

    fn language_server_workspace_configuration(
        &mut self,
        _language_server_id: &LanguageServerId,
        _worktree: &Worktree,
    ) -> Result<Option<serde_json::Value>> {
        Ok(Some(serde_json::json!({})))
    }
}

zed::register_extension!(AtmosExtension);

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_new_creates_extension_with_empty_cache() {
        let ext = AtmosExtension::new();
        assert!(ext.cached_binary_path.is_none());
    }

    #[test]
    fn test_language_server_command_caches_binary_path() {
        let mut ext = AtmosExtension::new();
        assert!(ext.cached_binary_path.is_none());

        // First resolution populates the cache.
        let result = ext.resolve_binary_path(|| Some("/usr/bin/atmos-lsp-bridge".to_string()));
        assert_eq!(result.unwrap(), "/usr/bin/atmos-lsp-bridge");
        assert_eq!(
            ext.cached_binary_path,
            Some("/usr/bin/atmos-lsp-bridge".to_string())
        );

        // Second resolution should return the cached path, ignoring the new lookup.
        let result = ext.resolve_binary_path(|| Some("/opt/bin/atmos-lsp-bridge".to_string()));
        assert_eq!(result.unwrap(), "/usr/bin/atmos-lsp-bridge");
        assert_eq!(
            ext.cached_binary_path,
            Some("/usr/bin/atmos-lsp-bridge".to_string())
        );
    }

    #[test]
    fn test_resolve_binary_path_errors_when_not_found() {
        let mut ext = AtmosExtension::new();
        let result = ext.resolve_binary_path(|| None);
        assert!(result.is_err());
        assert!(result.unwrap_err().contains("atmos-lsp-bridge not found"));
    }
}
