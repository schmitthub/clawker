# CLA Signature Store

Orphan branch that stores Contributor License Agreement signatures for clawker.

- Managed automatically by `.github/workflows/cla.yml` (contributor-assistant/github-action).
- Signatures are written to `signatures/version1/cla.json`, one entry per contributor.
- Intentionally has unrelated history — do NOT merge this branch into `main`.
- Do NOT delete it; the CLA status check reads its contents.

The agreement text lives in `CLA.md` on the default branch.
