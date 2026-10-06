import { useEffect, useRef, useState, type PointerEvent } from "react";
import { fit, scale, type Size, type View } from "./crop";

const box: Size = { w: 200, h: 300 };
// Largest output, 2:3 like the box; bigger sources are scaled down to fit.
const max: Size = { w: 2048, h: 3072 };

/** Crop picks a 2:3 area of an image by dragging and zooming, and hands back a PNG of it, at full resolution up to 2048x3072. */
export function Crop({
  src,
  busy,
  onDone,
  onError,
  onCancel,
}: {
  src: string;
  busy: boolean;
  onDone: (png: Blob) => void;
  onError: (message: string) => void;
  onCancel: () => void;
}) {
  const [img, setImg] = useState<HTMLImageElement | null>(null);
  const [broken, setBroken] = useState(false);
  const [view, setView] = useState<View>({ zoom: 1, dx: 0, dy: 0 });
  const [drag, setDrag] = useState<{ x: number; y: number } | null>(null);
  const dialog = useRef<HTMLDialogElement>(null);
  // showModal gives Escape, a focus trap, and focus back on close; jsdom lacks it.
  useEffect(() => {
    dialog.current?.showModal?.();
  }, []);
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
    const down = Math.min(1, max.w / rect.w, max.h / rect.h);
    const canvas = document.createElement("canvas");
    canvas.width = Math.round(rect.w * down);
    canvas.height = Math.round(rect.h * down);
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
    canvas.toBlob((b) => (b ? onDone(b) : onError("Couldn't crop the image.")), "image/png");
  };

  return (
    <dialog
      ref={dialog}
      className="crop"
      aria-label="Crop image"
      onCancel={(e) => {
        // Handle it ourselves so the parent unmounts us; the browser would only hide the dialog.
        e.preventDefault();
        if (!busy) onCancel();
      }}
    >
      <div
        className="crop-box"
        style={{ width: box.w, height: box.h }}
        onPointerDown={(e) => {
          if (e.button !== 0) return;
          e.currentTarget.setPointerCapture(e.pointerId);
          setDrag({ x: e.clientX, y: e.clientY });
        }}
        onPointerMove={move}
        onPointerUp={() => setDrag(null)}
        onPointerCancel={() => setDrag(null)}
      >
        <img
          src={src}
          alt=""
          draggable={false}
          onLoad={(e) => setImg(e.currentTarget)}
          onError={() => setBroken(true)}
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
      {broken && <p className="error">Couldn't load the image.</p>}
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
        <button className="button primary" disabled={!img || busy} onClick={save}>
          Save crop
        </button>
        <button className="button" disabled={busy} onClick={onCancel}>
          Cancel
        </button>
      </div>
    </dialog>
  );
}
