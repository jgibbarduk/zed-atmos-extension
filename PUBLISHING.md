# Publishing the Zed Atmos Extension

This guide covers how to publish the `atmos` extension to the Zed Extension Marketplace.

---

## Pre-requisites

- [GitHub](https://github.com) account
- [Zed](https://zed.dev) editor installed locally
- `rustup` with `wasm32-wasip2` target
- `cargo` and `pnpm` (or `npm`) installed

---

## Step 1 — Verify Local Dev Install

Before submitting, you **must** test the extension locally as a dev extension. Broken submissions are closed without review.

```bash
cd /Users/jamesgibbard/Development/zed-atmos-language

# Ensure the WASM target is installed
rustup target add wasm32-wasip2

# Build and install as a dev extension
zed --install-dev-extension .
```

Open any `.yaml` stack file in the test project and verify:

- **Hover** shows resolved imports, component definitions, accumulated vars, and stack name preview
- **Go-to-definition** (cmd+click) jumps from component names to their definitions and from imports to imported files
- **Rename** renames component references across the workspace
- **Diagnostics** appear in the workspace for issues like unresolvable imports, duplicate components, unknown template variables, etc.

To uninstall the dev extension:

```bash
zed --uninstall-extension atmos
```

---

## Step 2 — Commit and Push All Changes

Make sure `main` on your repo is up to date with all fixes.

```bash
git add -A
git commit -m "fix: wasip2 target, publishing blockers, proxy hangs, bridge-only mode"
git push origin main
```

---

## Step 3 — Fork the Zed Extensions Registry

Fork `https://github.com/zed-industries/extensions` to your **personal** GitHub account (`jgibbarduk`).

> **Important:** Fork to your personal account, not an organisation. Zed staff need permission to push fixes to your PR branch.

---

## Step 4 — Clone Your Fork

```bash
git clone https://github.com/jgibbarduk/extensions.git
cd extensions
```

---

## Step 5 — Add Your Extension as a Submodule

```bash
git submodule add https://github.com/jgibbarduk/zed-atmos-extension.git extensions/atmos
```

---

## Step 6 — Register the Extension

Edit the top-level `extensions.toml` in your fork and add:

```toml
[atmos]
submodule = "extensions/atmos"
version = "0.1.0"
```

Place it in alphabetical order among the existing entries.

---

## Step 7 — Sort Entries

```bash
pnpm install
pnpm sort-extensions
```

If `pnpm` is not available:

```bash
npm install
npx pnpm sort-extensions
```

This ensures `extensions.toml` and `.gitmodules` are in alphabetical order.

---

## Step 8 — Commit and Push

```bash
git add extensions.toml .gitmodules extensions/atmos
git commit -m "Add Atmos extension"
git push origin main
```

---

## Step 9 — Open a Pull Request

1. Go to `https://github.com/jgibbarduk/extensions`
2. Click **Compare & pull request**
3. Set the base repository to `zed-industries/extensions` and the base branch to `main`
4. Title: `Add Atmos extension`
5. Body: briefly describe what the extension does (Atmos stack configuration support with import resolution, inheritance navigation, diagnostics, and best-practice hints)
6. Submit the PR

---

## What Happens Next

Once the PR is merged, the Zed CI will:

1. Pull your repo as a submodule
2. Build the WASM extension (`cargo build --target wasm32-wasip2 --release`)
3. Package it into the Zed Extension Registry
4. Make it available in Zed's **Extensions** panel within minutes

Users can then install it directly from Zed via:
**Zed → Extensions → Search "Atmos" → Install**

---

## Quick Reference: Publishing Checklist

| # | Item | Status |
|---|------|--------|
| 1 | Valid open-source LICENSE at root | Done (MIT) |
| 2 | No `zed` / `Zed` / `extension` in ID or name | Done |
| 3 | Extension ID valid (`^[a-z0-9\-]+$`) | Done (`atmos`) |
| 4 | Grammar repos use HTTPS | Done |
| 5 | `extension.wasm` not committed to git | Done (in `.gitignore`) |
| 6 | `Cargo.lock` committed | Done |
| 7 | CI workflow exists and passes | Done |
| 8 | Release workflow produces artifacts | Done |
| 9 | README covers features, install, requirements | Done |
| 10 | `extension.toml` has all required fields | Done |
| 11 | Extension code implements `Extension` trait | Done |
| 12 | Binary is NOT bundled inside extension WASM | Done |
| 13 | WASM target updated to `wasm32-wasip2` | Done |
| 14 | `capabilities` args fixed in `extension.toml` | Done |
| 15 | Tested locally as a dev extension | **Do this now** |
| 16 | Fork `zed-industries/extensions` to personal account | **Do this now** |
| 17 | Add repo as HTTPS submodule in fork | **Do this now** |
| 18 | Add entry to top-level `extensions.toml` | **Do this now** |
| 19 | Run `pnpm sort-extensions` | **Do this now** |
| 20 | Open PR to `zed-industries/extensions` | **Do this now** |

---

## Troubleshooting

### `wasm32-wasip2` target not found

```bash
rustup target add wasm32-wasip2
```

If Zed still can't find it, ensure your Rust toolchain is up to date:

```bash
rustup update
```

### `pnpm sort-extensions` fails

Install Node dependencies first:

```bash
npm install
npx pnpm sort-extensions
```

### Extension not showing after PR merge

The registry updates automatically on merge. If it's not visible after ~10 minutes, check the [Actions tab](https://github.com/zed-industries/extensions/actions) of the `zed-industries/extensions` repo for build failures.

---

## Maintenance After Publishing

To release an update:

1. Bump the version in `extension.toml` and `Cargo.toml`
2. Tag the release: `git tag v0.2.0 && git push origin v0.2.0`
3. Open a new PR to `zed-industries/extensions` updating the `version` field in `extensions.toml`
