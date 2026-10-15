import { useRef } from "react";
import { macroParts } from "./macro";

/** MacroTextarea is a textarea whose macros get a tinted background, drawn by a mirror layer behind its transparent box. */
export function MacroTextarea(props: { label: string; value: string; onChange: (v: string) => void; start?: boolean }) {
  const mirror = useRef<HTMLDivElement>(null);
  return (
    <div className="macro">
      <div ref={mirror} className="input macro-mirror" aria-hidden="true">
        {macroParts(props.value, props.start).map((p, i) => (i % 2 ? <mark key={i}>{p}</mark> : p))}
        {/* A trailing empty line needs a character to take up height. */}
        {"\n"}
      </div>
      <textarea
        className="input grow"
        aria-label={props.label}
        value={props.value}
        onChange={(e) => props.onChange(e.target.value)}
        onScroll={(e) => {
          if (mirror.current) mirror.current.scrollTop = e.currentTarget.scrollTop;
        }}
      />
    </div>
  );
}
