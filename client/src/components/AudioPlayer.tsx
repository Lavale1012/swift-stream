import type { MouseEvent } from "react";
import { isExpired, type Audio } from "../useAudioJob";

interface AudioPlayerProps {
  audio: Audio;
  fileName: string;
  onRefresh: () => Promise<Audio | undefined>;
}

/** Plays and downloads the extracted MP3 from its presigned links. The links
 * expire (15 minutes), so both renew them when needed. */
export function AudioPlayer({ audio, fileName, onRefresh }: AudioPlayerProps) {
  async function download(e: MouseEvent<HTMLAnchorElement>) {
    if (!isExpired(audio)) return; // the link is still good; let the browser follow it
    e.preventDefault();
    const fresh = await onRefresh();
    // The link is signed with Content-Disposition: attachment, so the
    // browser saves the file and stays on this page.
    if (fresh) window.location.assign(fresh.downloadUrl);
  }

  return (
    <div className="player">
      <audio
        controls
        preload="metadata"
        src={audio.playUrl}
        aria-label={`Audio extracted from ${fileName}`}
        // Seeking makes new Range requests; once the link has expired those
        // fail with 403. Renew and the element reloads with the new src.
        onError={() => {
          if (isExpired(audio)) void onRefresh();
        }}
      />
      <a className="button" href={audio.downloadUrl} onClick={download}>
        Download MP3
      </a>
    </div>
  );
}
