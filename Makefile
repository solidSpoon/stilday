# The dev app: its own icon, name and identifier, with the inspector, in
# .mygo/dev. Unlike `mygo dev`, this does not watch anything: the app changes
# only when you run it. See README「运行」.
DEV_APP := .mygo/dev/$(shell go env GOOS)-$(shell go env GOARCH)/Stilday Dev.app

.PHONY: dev-app

dev-app:
	STILDAY_DEV=1 go tool mygo build -debug -skip-dmg
	-@killall -q "Stilday Dev"
	-@sleep 1
	@open "$(DEV_APP)"
