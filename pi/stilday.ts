/**
 * Stilday's Pi extension.
 *
 * It gives the model two tools around one day folder:
 *
 *   day_overview   the numbered contact sheets, plus the photo list
 *   stilday_emit   the model's JSONL output channel
 *
 * A detail is the read tool opening the photo's manifest read path: the
 * 1600px JPEG preview.
 *
 * The folder comes from STILDAY_DAY_DIR, set by the Stilday app. The app
 * writes manifest.json with the photos and sheets it prepared.
 */
import * as fs from "node:fs";
import * as path from "node:path";
import { defineTool, type ExtensionAPI } from "@earendil-works/pi-coding-agent";
import { Type } from "typebox";

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

const emit = defineTool({
	name: "stilday_emit",
	label: "Stilday Emit",
	description:
		"Record what you learned about the day. Each entry becomes one JSONL line in the user's context file. Only record things you actually saw in the photos or the conversation.",
	promptGuidelines: [
		"Call stilday_emit when you notice something worth keeping: an activity, a place, a person, a task, a mood, a decision.",
		"Each entry has a short kind and a one-sentence summary, plus optionally the photo index and time it came from.",
		"Do not invent events. Do not repeat the same observation in several entries.",
		"You may call stilday_emit more than once as the conversation develops.",
	],
	parameters: Type.Object({
		entries: Type.Array(
			Type.Object({
				kind: Type.String({
					description: "Short type, e.g. observation, place, activity, person, task, mood, decision.",
				}),
				summary: Type.String({ description: "One sentence, in the user's language." }),
				photo: Type.Optional(
					Type.Number({ description: "The photo index this observation comes from, if any." }),
				),
				occurred_at: Type.Optional(
					Type.String({ description: "RFC 3339 time the event happened, if known and not just the photo's." }),
				),
			}),
		),
	}),
	async execute(_toolCallId, params) {
		return {
			content: [{ type: "text", text: `Recorded ${params.entries.length} entries.` }],
			details: params,
		};
	},
});

export default function (pi: ExtensionAPI) {
	pi.registerTool(overview);
	pi.registerTool(emit);
}
