import { useState } from "react";
import { ACCEPTED_TYPES } from "../api";

const ACCEPT = Object.entries(ACCEPTED_TYPES).flat().join(",");

/** A file input styled as a drop target. The input stays in the tab order,
 * so the zone works from the keyboard. */
export function Dropzone({ onFile }: { onFile: (file: File) => void }) {
  const [dragging, setDragging] = useState(false);

  return (
    <label
      className="dropzone"
      data-dragging={dragging || undefined}
      onDragOver={(e) => {
        e.preventDefault();
        setDragging(true);
      }}
      onDragLeave={() => setDragging(false)}
      onDrop={(e) => {
        e.preventDefault();
        setDragging(false);
        const file = e.dataTransfer.files[0];
        if (file) onFile(file);
      }}
    >
      <input
        type="file"
        className="visually-hidden"
        accept={ACCEPT}
        onChange={(e) => {
          const file = e.target.files?.[0];
          // Clear it so choosing the same file again still fires onChange.
          e.target.value = "";
          if (file) onFile(file);
        }}
      />
      <span className="dropzone-title">Choose a video</span>
      <span className="dropzone-hint">or drop one here. MP4, MOV, WebM or MKV, up to 1 GB.</span>
    </label>
  );
}
