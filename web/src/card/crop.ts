export type Size = { w: number; h: number };
/** View is a zoom (1 = the image just covers the box) and the image center's offset from the box center, in screen pixels. */
export type View = { zoom: number; dx: number; dy: number };
type Rect = { x: number; y: number; w: number; h: number };

/** scale is how many screen pixels one image pixel takes at a zoom. */
export const scale = (img: Size, box: Size, zoom: number) => Math.max(box.w / img.w, box.h / img.h) * zoom;

/** fit clamps a view so the image covers the box, and returns the image area the box shows. */
export function fit(img: Size, box: Size, v: View): { view: View; rect: Rect } {
  const zoom = Math.max(1, v.zoom);
  const s = scale(img, box, zoom);
  const maxX = (img.w * s - box.w) / 2;
  const maxY = (img.h * s - box.h) / 2;
  const dx = Math.min(maxX, Math.max(-maxX, v.dx));
  const dy = Math.min(maxY, Math.max(-maxY, v.dy));
  return {
    view: { zoom, dx, dy },
    rect: { x: img.w / 2 - (box.w / 2 + dx) / s, y: img.h / 2 - (box.h / 2 + dy) / s, w: box.w / s, h: box.h / s },
  };
}
