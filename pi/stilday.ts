/**
 * Stilday's Pi extension.
 *
 * It gives the model two tools around one day folder:
 *
 *   day_overview   the numbered contact sheets, plus the photo list
 *   stilday_save_day   the day's log, saved as a whole
 *
 * A detail is the read tool opening the photo's manifest read path: the
 * 1600px JPEG preview.
 *
 * The folder comes from STILDAY_DAY_DIR, set by the Stilday app. The app
 * writes manifest.json with the photos and sheets it prepared.
 *
 * stilday_save_day's arguments arrive as JSON Schema in
 * STILDAY_DAY_LOG_SCHEMA: they are declared in the app (day_log_schema.go)
 * and rendered into both the tool's parameters and the prompt's 记录
 * section, so the two agree.
 */
import * as fs from "node:fs";
import * as path from "node:path";
import { defineTool, type ExtensionAPI } from "@earendil-works/pi-coding-agent";
import { Type, type TSchema } from "typebox";

type Manifest = {
	day: string;
	photos: {
		index: number;
		preview: string;
		thumb: string;
		read: string;
		captured_at: string;
		sheet: number;
		sheet_file: string;
		cell: number;
		cell_row: number;
		cell_col: number;
		width: number;
		height: number;
		format: string;
	}[];
	files?: {
		path: string;
		kind: string;
		photo?: number;
		sheet?: number;
	}[];
	sheets: {
		index: number;
		file: string;
		from: string;
		to: string;
		first: number;
		last: number;
		photos: number[];
	}[];
};

const dayDir = process.env.STILDAY_DAY_DIR ?? "";

function readManifest(): Manifest {
	if (!dayDir) throw new Error("STILDAY_DAY_DIR is not set");
	const p = path.join(dayDir, "manifest.json");
	return JSON.parse(fs.readFileSync(p, "utf8")) as Manifest;
}

function clock(rfc3339: string): string {
	// Go writes local time with its offset, so the wall clock is in the
	// string already.
	const m = /T(\d{2}:\d{2})/.exec(rfc3339);
	return m ? m[1] : rfc3339;
}

function imagePart(file: string) {
	return {
		type: "image" as const,
		data: fs.readFileSync(path.join(dayDir, file)).toString("base64"),
		mimeType: "image/jpeg",
	};
}

function photoList(m: Manifest): string {
	return m.photos.map((p) => `#${p.index} ${clock(p.captured_at)}`).join("  ·  ");
}

const overview = defineTool({
	name: "day_overview",
	label: "Day Overview",
	description:
		"Show the day's numbered contact sheets: the fastest way to see what happened. Each thumbnail is labeled #index and HH:MM. The photos are already attached to the first message of the day; call this again if you need to look at them later.",
	promptGuidelines: [
		"Use day_overview to see the day's photos as a whole before answering questions about the day.",
		"The numbers on the sheets are the photo indexes. For a close-up, read the photo's read path from manifest.json with the read tool. It is the 1600px JPEG preview. The manifest maps each sheet cell to its photo and gives its time and size.",
	],
	parameters: Type.Object({}),
	async execute() {
		const m = readManifest();
		const content: any[] = [
			{
				type: "text",
				text:
					`Day ${m.day}: ${m.photos.length} photos in ${m.sheets.length} contact sheets. ` +
					`Photo numbers: ${photoList(m)}. ` +
					`For a close-up, read the photo's read path from manifest.json with the read tool (the 1600px JPEG preview); ` +
					`manifest.json maps each sheet cell to its photo and lists its time and size.`,
			},
		];
		for (const s of m.sheets) {
			content.push({
				type: "text",
				text: `Sheet ${s.index + 1}/${m.sheets.length}: ${clock(s.from)}–${clock(s.to)}, photos #${s.first}–#${s.last}.`,
			});
			content.push(imagePart(s.file));
		}
		return { content, details: { day: m.day, photos: m.photos.length, sheets: m.sheets.length } };
	},
});

// The subset of JSON Schema day_log_schema.go writes: ordered properties,
// required, items, and a description on every node.
type JSONSchema = {
	type: "object" | "array" | "string" | "number" | "boolean";
	description?: string;
	enum?: string[];
	properties?: Record<string, JSONSchema>;
	required?: string[];
	items?: JSONSchema;
};

function typeBox(spec: JSONSchema): TSchema {
	const opts = spec.description ? { description: spec.description } : {};
	switch (spec.type) {
		case "object": {
			const required = spec.required ?? [];
			const properties: Record<string, TSchema> = {};
			// Objects keep insertion order, so the fields reach the model in
			// the same order as the prompt.
			for (const [name, child] of Object.entries(spec.properties ?? {})) {
				const type = typeBox(child);
				properties[name] = required.includes(name) ? type : Type.Optional(type);
			}
			return Type.Object(properties, opts);
		}
		case "array":
			return Type.Array(typeBox(spec.items!), opts);
		case "number":
			return Type.Number(opts);
		case "boolean":
			return Type.Boolean(opts);
		case "string":
			return spec.enum
				? Type.Union(spec.enum.map((value) => Type.Literal(value)), opts)
				: Type.String(opts);
		default:
			return Type.String(opts);
	}
}

function dayLogParameters(): TSchema {
	const raw = process.env.STILDAY_DAY_LOG_SCHEMA;
	if (!raw) throw new Error("STILDAY_DAY_LOG_SCHEMA is not set; Stilday starts Pi with it");
	return typeBox(JSON.parse(raw) as JSONSchema);
}

const saveDay = defineTool({
	name: "stilday_save_day",
	label: "Save Day",
	description:
		"Save the day's log: the complete list of entries. Every call replaces what the day had saved before, so send the whole list, including the entries that did not change.",
	promptGuidelines: [
		"Call stilday_save_day with the day's complete log: the first analysis sends everything you found, and a later change sends the whole list again.",
		"Every call replaces the day's log, so never send only the new or changed entries.",
		"Each entry has a short kind and a one-sentence summary, plus optionally the photo index and time it came from.",
		"Do not invent events. Do not repeat the same observation in several entries.",
	],
	parameters: dayLogParameters(),
	async execute(_toolCallId, params) {
		// The schema is built at runtime, so the arguments are untyped here.
		const entries = (params as { entries: unknown[] }).entries;
		return {
			content: [{ type: "text", text: `Saved ${entries.length} entries.` }],
			details: params,
		};
	},
});

export default function (pi: ExtensionAPI) {
	pi.registerTool(overview);
	pi.registerTool(saveDay);
}
