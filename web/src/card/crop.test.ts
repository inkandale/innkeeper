import { fit } from "./crop";

const img = { w: 400, h: 400 };
const box = { w: 200, h: 300 };

test("at zoom 1 the image covers the box, centered", () => {
  const { view, rect } = fit(img, box, { zoom: 1, dx: 0, dy: 0 });
  expect(view).toEqual({ zoom: 1, dx: 0, dy: 0 });
  expect(rect.x).toBeCloseTo(66.67, 1);
  expect(rect.y).toBeCloseTo(0);
  expect(rect.w).toBeCloseTo(266.67, 1);
  expect(rect.h).toBeCloseTo(400);
});

test("panning stops at the image's edges", () => {
  const { view, rect } = fit(img, box, { zoom: 1, dx: 1000, dy: 50 });
  expect(view).toEqual({ zoom: 1, dx: 50, dy: 0 });
  expect(rect.x).toBeCloseTo(0);
});

test("zooming in shows a smaller area, and zoom never drops below 1", () => {
  expect(fit(img, box, { zoom: 2, dx: 0, dy: 0 }).rect.h).toBeCloseTo(200);
  expect(fit(img, box, { zoom: 0.5, dx: 0, dy: 0 }).view.zoom).toBe(1);
});
