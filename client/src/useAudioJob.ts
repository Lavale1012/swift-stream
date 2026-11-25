import { useRef, useState } from "react";
import {
  ClientError,
  MAX_UPLOAD_BYTES,
  NotReady,
  contentTypeOf,
  getAudio,
  isAbort,
  requestUpload,
  uploadToS3,
  type AudioUrls,
} from "./api";
import { formatBytes } from "./format";

const POLL_INTERVAL_MS = 2_000;
// A failed attempt is retried when SQS redelivers the message, about two
// minutes later (the queue's visibility timeout). A job that has stayed
// failed for longer than that has most likely run out of retries.
const GIVE_UP_AFTER_FAILED_MS = 3 * 60_000;
const GIVE_UP_AFTER_MS = 15 * 60_000;
// Renew presigned links this long before they expire.
const EXPIRY_MARGIN_MS = 30_000;

export type Audio = AudioUrls & { expiresAt: number };

export type Phase =
  | { step: "choose" }
  | { step: "upload"; file: File; loaded: number; total: number }
  | { step: "extract"; file: File; jobId: string; status: string }
  | { step: "listen"; file: File; jobId: string; audio: Audio }
  | { step: "failed"; at: "upload"; file: File; message: string; retryable: boolean }
  | { step: "failed"; at: "extract"; file: File; jobId: string; message: string };

export function isExpired(audio: Audio): boolean {
  return Date.now() >= audio.expiresAt - EXPIRY_MARGIN_MS;
}

function withExpiry(urls: AudioUrls): Audio {
  return { ...urls, expiresAt: Date.now() + urls.expiresIn * 1000 };
}

function messageOf(err: unknown): string {
  return err instanceof ClientError ? err.message : "Something unexpected went wrong. The browser console has details.";
}

function sleep(ms: number, signal: AbortSignal): Promise<void> {
  return new Promise((resolve, reject) => {
    const timer = setTimeout(resolve, ms);
    signal.addEventListener(
      "abort",
      () => {
        clearTimeout(timer);
        reject(signal.reason);
      },
      { once: true },
    );
  });
}

/** Polls the audio endpoint until the job completes. The API answers 409
 * while the job is pending, processing or failed. */
async function pollForAudio(
  jobId: string,
  signal: AbortSignal,
  onStatus: (status: string) => void,
): Promise<AudioUrls> {
  const startedAt = Date.now();
  let failedSince: number | undefined;
  for (;;) {
    try {
      return await getAudio(jobId, signal);
    } catch (err) {
      if (!(err instanceof NotReady)) throw err;
      onStatus(err.status);
      const now = Date.now();
      if (err.status === "failed") {
        failedSince ??= now;
        if (now - failedSince > GIVE_UP_AFTER_FAILED_MS) {
          throw new ClientError("Extraction failed and wasn't retried. The worker's logs have the reason.");
        }
      } else {
        failedSince = undefined;
      }
      if (now - startedAt > GIVE_UP_AFTER_MS) {
        throw new ClientError("The audio still isn't ready after 15 minutes. Check that a worker is running.");
      }
    }
    await sleep(POLL_INTERVAL_MS, signal);
  }
}

/** Drives one video through upload → extraction → playback. Starting a new
 * action cancels whatever was in flight. */
export function useAudioJob() {
  const [phase, setPhase] = useState<Phase>({ step: "choose" });
  const inFlight = useRef<AbortController | null>(null);

  function takeOver(): AbortSignal {
    inFlight.current?.abort();
    inFlight.current = new AbortController();
    return inFlight.current.signal;
  }

  async function extract(file: File, jobId: string, signal: AbortSignal) {
    setPhase({ step: "extract", file, jobId, status: "pending" });
    try {
      const urls = await pollForAudio(jobId, signal, (status) =>
        setPhase({ step: "extract", file, jobId, status }),
      );
      setPhase({ step: "listen", file, jobId, audio: withExpiry(urls) });
    } catch (err) {
      if (isAbort(err)) return;
      setPhase({ step: "failed", at: "extract", file, jobId, message: messageOf(err) });
    }
  }

  async function start(file: File) {
    const signal = takeOver();
    const contentType = contentTypeOf(file);
    const reject = (message: string) =>
      setPhase({ step: "failed", at: "upload", file, message, retryable: false });
    if (!contentType) return reject("That file type isn't supported. Choose an MP4, MOV, WebM or MKV video.");
    if (file.size === 0) return reject("That file is empty. Choose another video.");
    if (file.size > MAX_UPLOAD_BYTES) {
      return reject(`That video is ${formatBytes(file.size)}. The limit is 1 GB.`);
    }

    setPhase({ step: "upload", file, loaded: 0, total: file.size });
    let jobId: string;
    try {
      const ticket = await requestUpload(contentType, file.size);
      await uploadToS3(
        ticket,
        file,
        (loaded, total) => setPhase({ step: "upload", file, loaded, total }),
        signal,
      );
      jobId = ticket.jobId;
    } catch (err) {
      if (isAbort(err)) return;
      setPhase({ step: "failed", at: "upload", file, message: messageOf(err), retryable: true });
      return;
    }
    await extract(file, jobId, signal);
  }

  function checkAgain(file: File, jobId: string) {
    void extract(file, jobId, takeOver());
  }

  /** Fetches fresh presigned links for a finished job. */
  async function refreshAudio(file: File, jobId: string): Promise<Audio | undefined> {
    try {
      const audio = withExpiry(await getAudio(jobId, takeOver()));
      setPhase({ step: "listen", file, jobId, audio });
      return audio;
    } catch (err) {
      if (isAbort(err)) return undefined;
      setPhase({ step: "failed", at: "extract", file, jobId, message: messageOf(err) });
      return undefined;
    }
  }

  function reset() {
    inFlight.current?.abort();
    inFlight.current = null;
    setPhase({ step: "choose" });
  }

  return { phase, start, checkAgain, refreshAudio, reset };
}
