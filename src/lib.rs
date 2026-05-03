use zed_extension_api::{self as zed, Command, Extension, LanguageServerId, Result, Worktree};

struct AtmosExtension;

impl Extension for AtmosExtension {
    fn new() -> Self {
        AtmosExtension
    }

    fn language_server_command(
        &mut self,
        // Unused: this extension only registers a single language server, so we do not
        // need to dispatch on the server ID.
        _language_server_id: &LanguageServerId,
        worktree: &Worktree,
    ) -> Result<Command> {
        let binary_path = worktree
            .which("atmos-lsp-bridge")
            .ok_or_else(|| "atmos-lsp-bridge not found in PATH. Install it from https://github.com/jamesgibbard/zed-atmos-language/releases".to_string())?;

        Ok(Command {
            command: binary_path,
            args: vec![],
            env: vec![],
        })
    }
}

zed::register_extension!(AtmosExtension);

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_new_creates_extension() {
        let _ext = AtmosExtension::new();
    }
}
