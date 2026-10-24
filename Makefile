# The dev app: its own icon, name and identifier, with the inspector, in
# .mygo/dev. Unlike `mygo dev`, this does not watch anything: the app changes
# only when you run it. See README「运行」.
DEV_APP := .mygo/dev/$(shell go env GOOS)-$(shell go env GOARCH)/Stilday Dev.app

# The release that `make release` cuts: the version from mygo.config.ts, and
# the dmg mygo build writes for this machine.
VERSION := $(shell sed -n 's/.*version: "\([^"]*\)".*/\1/p' mygo.config.ts)
DMG := build/$(shell go env GOOS)-$(shell go env GOARCH)/Stilday $(VERSION) $(shell go env GOARCH).dmg

.PHONY: dev-app release

dev-app:
	STILDAY_DEV=1 go tool mygo build -debug -skip-dmg
	-@killall -q "Stilday Dev"
	-@sleep 1
	@open "$(DEV_APP)"

# Cut a release: bump version in mygo.config.ts, commit, and run this. It
# tests, builds the dmg, tags it, pushes main and the tag, then opens a
# GitHub release whose notes GitHub generates from the commits.
release:
	@test -n "$(VERSION)" || (echo "no version in mygo.config.ts"; exit 1)
	@test "$$(git rev-parse --abbrev-ref HEAD)" = main || (echo "release from main"; exit 1)
	@test -z "$$(git status --porcelain)" || (echo "the working tree is not clean; commit first"; exit 1)
	@! git rev-parse -q --verify "refs/tags/v$(VERSION)" >/dev/null || (echo "tag v$(VERSION) already exists"; exit 1)
	go test ./...
	go tool mygo build
	git tag -a "v$(VERSION)" -m "Release $(VERSION)"
	git push origin main
	git push origin "v$(VERSION)"
	gh release create "v$(VERSION)" "$(DMG)" --title "Stilday $(VERSION)" --generate-notes
