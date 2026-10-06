import { useState, type PointerEvent } from "react";
import { fit, scale, type Size, type View } from "./crop";

const box: Size = { w: 200, h: 300 };

/** Crop picks a 2:3 area of an image by dragging and zooming, and hands back a PNG of it at full resolution. */
export function Crop({
  src,
  onDone,
  onCancel,
}: {
  src: string;
  onDone: (png: Blob) => void;
  onCancel: () => void;
}) {
  const [img, setImg] = useState<HTMLImageElement | null>(null);
  const [view, setView] = useState<View>({ zoom: 1, dx: 0, dy: 0 });
  const [drag, setDrag] = useState<{ x: number; y: number } | null>(null);
  const size = img ? { w: img.naturalWidth, h: img.naturalHeight } : null;
  const set = (v: View) => size && setView(fit(size, box, v).view);
  const s = size ? scale(size, box, view.zoom) : 1;

  const move = (e: PointerEvent) => {
    if (!drag) return;
    set({
      ...view,
      dx: view.dx + e.clientX - drag.x,
      dy: view.dy + e.clientY - drag.y,
    });
    setDrag({ x: e.clientX, y: e.clientY });
  };
  const save = () => {
    if (!img || !size) return;
    const { rect } = fit(size, box, view);
    const canvas = document.createElement("canvas");
    canvas.width = Math.round(rect.w);
    canvas.height = Math.round(rect.h);
    canvas
      .getContext("2d")
      ?.drawImage(
        img,
        rect.x,
        rect.y,
        rect.w,
        rect.h,
        0,
        0,
        canvas.width,
        canvas.height,
      );
    canvas.toBlob((b) => b && onDone(b), "image/png");
  };

  return (
    <div className="crop-backdrop">
      <div
        className="crop"
        role="dialog"
        aria-modal="true"
        aria-label="Crop image"
      >
        <div
          className="crop-box"
          style={{ width: box.w, height: box.h }}
          onPointerDown={(e) => {
            e.currentTarget.setPointerCapture(e.pointerId);
            setDrag({ x: e.clientX, y: e.clientY });
          }}
          onPointerMove={move}
          onPointerUp={() => setDrag(null)}
        >
          <img
            src={src}
            alt=""
            draggable={false}
            onLoad={(e) => setImg(e.currentTarget)}
            style={
              size
                ? {
                    width: size.w * s,
                    height: size.h * s,
                    transform: `translate(calc(-50% + ${view.dx}px), calc(-50% + ${view.dy}px))`,
                  }
                : undefined
            }
          />
        </div>
        <label>
          Zoom{" "}
          <input
            type="range"
            min={1}
            max={4}
            step={0.05}
            value={view.zoom}
            onChange={(e) => set({ ...view, zoom: Number(e.target.value) })}
          />
        </label>
        <div className="row">
          <button className="button primary" disabled={!img} onClick={save}>
            Save crop
          </button>
          <button className="button" onClick={onCancel}>
            Cancel
          </button>
        </div>
      </div>
    </div>
  );
}
