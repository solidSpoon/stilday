// mygo's configuration. `mygo dev` adds " Dev" and ".dev" to the name and
// identifier itself; STILDAY_DEV=1 only exists so `make dev-app` can build
// that same app once, without the file watcher.
const dev = process.env.STILDAY_DEV === "1";

export default {
	name: dev ? "Stilday Dev" : "Stilday",
	identifier: dev ? "com.example.stilday.dev" : "com.example.stilday",
	version: "0.2.0",
	out: dev ? ".mygo/dev" : "build",
	...(dev ? { icon: "resources/icon-dev.png" } : {}),
};
