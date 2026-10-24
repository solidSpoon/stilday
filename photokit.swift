import Foundation
import Photos
import CoreGraphics
import ImageIO

struct OutAsset: Codable {
    let id: String
    let file: String
    let filename: String
    let time: Double
    let capture: Double?
    let width: Int
    let height: Int
    let favorite: Bool
    let hidden: Bool
    let albums: [String]
    let latitude: Double?
    let longitude: Double?
    let modified: Double?
    let source: String
    let live: Bool
    let screenshot: Bool
    let panorama: Bool
    let hdr: Bool
    let depth: Bool
    let burst: Bool
    let adjustments: Bool
}

struct Out: Codable {
    var ok: Bool
    var error: String?
    var assets: [OutAsset]
}

@main
struct StildayPhotos {
    static func main() {
        Task { await run() }
        dispatchMain()
    }

    static func writeJSON(_ out: Out, dir: String) {
        let url = URL(fileURLWithPath: dir).appendingPathComponent("assets.json")
        if let data = try? JSONEncoder().encode(out) {
            try? data.write(to: url)
        }
    }

    static func sourceName(_ source: PHAssetSourceType) -> String {
        switch source {
        case .typeUserLibrary: return "library"
        case .typeCloudShared: return "shared"
        case .typeiTunesSynced: return "itunes"
        default: return "other"
        }
    }

    static func albumMembership() -> [String: [String]] {
        var map: [String: [String]] = [:]
        let albums = PHAssetCollection.fetchAssetCollections(with: .album, subtype: .any, options: nil)
        albums.enumerateObjects { collection, _, _ in
            let title = collection.localizedTitle ?? "未命名相册"
            let members = PHAsset.fetchAssets(in: collection, options: nil)
            members.enumerateObjects { asset, _, _ in
                map[asset.localIdentifier, default: []].append(title)
            }
        }
        return map
    }

    static func run() async {
        let args = CommandLine.arguments
        guard args.count >= 5,
              let start = Double(args[1]),
              let end = Double(args[2]),
              let maxAssets = Int(args[4]) else {
            writeJSON(Out(ok: false, error: "usage: start end outDir max", assets: []), dir: args.count > 3 ? args[3] : NSTemporaryDirectory())
            exit(2)
        }
        let outDir = args[3]
        try? FileManager.default.createDirectory(atPath: outDir, withIntermediateDirectories: true)

        let status = await withCheckedContinuation { (cont: CheckedContinuation<PHAuthorizationStatus, Never>) in
            PHPhotoLibrary.requestAuthorization(for: .readWrite) { s in cont.resume(returning: s) }
        }
        guard status == .authorized || status == .limited else {
            writeJSON(Out(ok: false, error: "photo library access denied", assets: []), dir: outDir)
            exit(3)
        }

        let options = PHFetchOptions()
        options.predicate = NSPredicate(
            format: "mediaType == %d AND creationDate >= %@ AND creationDate < %@",
            PHAssetMediaType.image.rawValue,
            Date(timeIntervalSince1970: start) as NSDate,
            Date(timeIntervalSince1970: end) as NSDate)
        options.sortDescriptors = [NSSortDescriptor(key: "creationDate", ascending: true)]
        let result = PHAsset.fetchAssets(with: options)
        var assets: [PHAsset] = []
        result.enumerateObjects { asset, _, _ in
            if maxAssets <= 0 || assets.count < maxAssets {
                assets.append(asset)
            }
        }

        let albums = albumMembership()
        var out: [OutAsset] = []
        for (index, asset) in assets.enumerated() {
            guard let file = await export(asset, index: index, dir: outDir) else { continue }
            let resource = PHAssetResource.assetResources(for: asset).first
            let sub = asset.mediaSubtypes
            let coord = asset.location?.coordinate
            out.append(OutAsset(
                id: asset.localIdentifier,
                file: file,
                filename: resource?.originalFilename ?? file,
                time: asset.creationDate?.timeIntervalSince1970 ?? 0,
                capture: captureDate(file, dir: outDir),
                width: asset.pixelWidth,
                height: asset.pixelHeight,
                favorite: asset.isFavorite,
                hidden: asset.isHidden,
                albums: albums[asset.localIdentifier] ?? [],
                latitude: coord?.latitude,
                longitude: coord?.longitude,
                modified: asset.modificationDate?.timeIntervalSince1970,
                source: sourceName(asset.sourceType),
                live: sub.contains(.photoLive),
                screenshot: sub.contains(.photoScreenshot),
                panorama: sub.contains(.photoPanorama),
                hdr: sub.contains(.photoHDR),
                depth: sub.contains(.photoDepthEffect),
                burst: asset.representsBurst || asset.burstIdentifier != nil,
                adjustments: asset.hasAdjustments))
        }
        writeJSON(Out(ok: true, error: nil, assets: out), dir: outDir)
        exit(0)
    }

    static func captureDate(_ file: String, dir: String) -> Double? {
        let url = URL(fileURLWithPath: dir).appendingPathComponent(file) as CFURL
        guard let source = CGImageSourceCreateWithURL(url, nil),
              let props = CGImageSourceCopyPropertiesAtIndex(source, 0, nil) as? [CFString: Any] else {
            return nil
        }
        var text: String?
        if let exif = props[kCGImagePropertyExifDictionary] as? [CFString: Any] {
            text = exif[kCGImagePropertyExifDateTimeOriginal] as? String
        }
        if text == nil, let tiff = props[kCGImagePropertyTIFFDictionary] as? [CFString: Any] {
            text = tiff[kCGImagePropertyTIFFDateTime] as? String
        }
        guard let text else { return nil }
        let formatter = DateFormatter()
        formatter.dateFormat = "yyyy:MM:dd HH:mm:ss"
        formatter.timeZone = .current
        guard let date = formatter.date(from: text) else { return nil }
        return date.timeIntervalSince1970
    }

    static func export(_ asset: PHAsset, index: Int, dir: String) async -> String? {
        let options = PHImageRequestOptions()
        options.isNetworkAccessAllowed = true
        options.deliveryMode = .highQualityFormat
        options.resizeMode = .none
        options.version = .original
        let resource = PHAssetResource.assetResources(for: asset).first
        let name = resource?.originalFilename ?? "asset.jpg"
        var ext = (name as NSString).pathExtension.lowercased()
        if ext.isEmpty { ext = "jpg" }
        if ext == "jpeg" { ext = "jpg" }
        let file = String(format: "%04d.%@", index + 1, ext)
        let url = URL(fileURLWithPath: dir).appendingPathComponent(file)
        return await withCheckedContinuation { (cont: CheckedContinuation<String?, Never>) in
            PHImageManager.default().requestImageDataAndOrientation(for: asset, options: options) { data, _, _, _ in
                guard let data else { cont.resume(returning: nil); return }
                do {
                    try data.write(to: url)
                    cont.resume(returning: file)
                } catch {
                    cont.resume(returning: nil)
                }
            }
        }
    }
}
