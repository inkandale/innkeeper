import { useRef, type ReactNode, type Ref, type TextareaHTMLAttributes } from "react";
import { macroParts } from "./macro";

type MirroredProps = TextareaHTMLAttributes<HTMLTextAreaElement> & { mirror: ReactNode; ref?: Ref<HTMLTextAreaElement> };

/** Mirrored is a transparent textarea over a layer that draws mirror, the same text with parts marked. */
export function Mirrored({ mirror, className, onScroll, ref, ...props }: MirroredProps) {
  const layer = useRef<HTMLDivElement>(null);
  return (
    <div className="macro">
      <div ref={layer} className={`input macro-mirror ${className ?? ""}`} aria-hidden="true">
        {mirror}
        {/* A trailing empty line needs a character to take up height. */}
        {"\n"}
      </div>
      <textarea
        ref={ref}
        className={`input grow ${className ?? ""}`}
        {...props}
        onScroll={(e) => {
          if (layer.current) layer.current.scrollTop = e.currentTarget.scrollTop;
          onScroll?.(e);
        }}
      />
    </div>
  );
}

/** MacroTextarea is a textarea whose macros get a tinted background. */
export function MacroTextarea(props: { label: string; value: string; onChange: (v: string) => void; start?: boolean }) {
  return (
    <Mirrored
      aria-label={props.label}
      value={props.value}
      onChange={(e) => props.onChange(e.target.value)}
      mirror={macroParts(props.value, props.start).map((p, i) => (i % 2 ? <mark key={i}>{p}</mark> : p))}
    />
  );
}
