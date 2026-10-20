# Releasing

Default branch: `master`.

## Check and package

```sh
python -B scripts/validate_skill.py --skill-dir .
python -B tests/run_all.py --report ../validation.json
python -B scripts/package_skill.py --source . --tag v2.1.0 --output ../prototype-first-ui.zip
```

## Publish

Set `metadata.version` in `SKILL.md` and add the matching entry to `CHANGELOG.md`.
Commit the changes, then push the branch and version tag:

```sh
git push origin master
git tag -a v2.1.0 -m "Release v2.1.0"
git push origin v2.1.0
```

[GitHub Actions](.github/workflows/ci.yml) tests the extracted package and publishes
its ZIP, SHA-256 checksum, and test reports when all checks pass.
