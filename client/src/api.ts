// The client's side of the API in server/. Field names match the Pydantic
// models in server/app/routes/uploads.py and server/app/routes/media.py.

const API_URL = (import.meta.env.VITE_API_URL ?? "http://localhost:8080").replace(/\/$/, "");

// Keep in step with UPLOAD_EXTENSIONS and MAX_UPLOAD_BYTES in
// server/app/handlers/uploads.py. The server is the real check; these only
// save a round trip and give a clearer message.
export const ACCEPTED_TYPES: Record<string, string> = {
  "video/mp4": ".mp4",
  "video/quicktime": ".mov",
  "video/webm": ".webm",
  "video/x-matroska": ".mkv",
};
export const MAX_UPLOAD_BYTES = 1 << 30; // 1 GiB

export interface UploadTicket {
  jobId: string;
  url: string;
  method: string;
  headers: Record<string, string>;
  key: string;
  expiresIn: number;
}

export interface AudioUrls {
  playUrl: string;
  downloadUrl: string;
  expiresIn: number;
}

/** An error with a message written for the person using the page. */
export class ClientError extends Error {}

/** Thrown by getAudio while the job exists but has no audio yet (HTTP 409). */
export class NotReady extends Error {
  readonly status: string;
  constructor(status: string) {
    super(`job is ${status}`);
    this.status = status;
  }
}

export function isAbort(err: unknown): boolean {
  return err instanceof DOMException && err.name === "AbortError";
}

/** The content type to sign. Browsers often leave File.type empty for
 * .mkv (and sometimes .mov), so fall back to the file extension. */
export function contentTypeOf(file: File): string | undefined {
  if (file.type in ACCEPTED_TYPES) return file.type;
  const ext = file.name.slice(file.name.lastIndexOf(".")).toLowerCase();
  return Object.keys(ACCEPTED_TYPES).find((type) => ACCEPTED_TYPES[type] === ext);
}

async function detailOf(res: Response): Promise<string> {
  try {
    const body = await res.json();
    return typeof body.detail === "string" ? body.detail : JSON.stringify(body.detail);
  } catch {
    return res.statusText;
  }
}

async function call(path: string, init?: RequestInit): Promise<Response> {
  try {
    return await fetch(`${API_URL}${path}`, init);
  } catch (err) {
    if (isAbort(err)) throw err;
    // fetch rejects without a status for both "server down" and "CORS
    // blocked"; DevTools' console says which.
    throw new ClientError(
      `Can't reach the API at ${API_URL}. Check that it's running and that ${window.location.origin} is in CORS_ALLOWED_ORIGINS.`,
    );
  }
}

export async function requestUpload(contentType: string, size: number): Promise<UploadTicket> {
  const res = await call("/upload", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ contentType, size }),
  });
  if (!res.ok) {
    throw new ClientError(`The API refused the upload (${res.status}): ${await detailOf(res)}`);
  }
  return res.json();
}

/** PUTs the file to the presigned S3 URL. XMLHttpRequest rather than fetch,
 * because fetch can't report upload progress. */
export function uploadToS3(
  ticket: UploadTicket,
  file: File,
  onProgress: (loaded: number, total: number) => void,
  signal: AbortSignal,
): Promise<void> {
  return new Promise((resolve, reject) => {
    if (signal.aborted) return reject(signal.reason);
    const xhr = new XMLHttpRequest();
    xhr.open(ticket.method, ticket.url);
    for (const [name, value] of Object.entries(ticket.headers)) {
      xhr.setRequestHeader(name, value);
    }
    xhr.upload.onprogress = (e) => {
      if (e.lengthComputable) onProgress(e.loaded, e.total);
    };
    xhr.onload = () => {
      if (xhr.status >= 200 && xhr.status < 300) return resolve();
      // S3 answers with an XML body; its <Code> names the problem.
      const code = /<Code>([^<]+)<\/Code>/.exec(xhr.responseText)?.[1];
      if (code === "AccessDenied" && /expired/i.test(xhr.responseText)) {
        return reject(new ClientError("The upload link expired before the upload finished. Start again."));
      }
      reject(new ClientError(`S3 rejected the upload (${xhr.status}${code ? ` ${code}` : ""}).`));
    };
    // Status 0 with no response: the request never got an answer. With S3
    // that's almost always the bucket's CORS rule missing this origin.
    xhr.onerror = () =>
      reject(
        new ClientError(
          `The upload couldn't reach S3. Check the upload bucket's CORS rule allows ${window.location.origin}.`,
        ),
      );
    xhr.onabort = () => reject(new DOMException("Upload cancelled", "AbortError"));
    signal.addEventListener("abort", () => xhr.abort(), { once: true });
    xhr.send(file);
  });
}

export async function getAudio(jobId: string, signal?: AbortSignal): Promise<AudioUrls> {
  const res = await call(`/jobs/${jobId}/audio`, { signal });
  if (res.ok) return res.json();
  const detail = await detailOf(res);
  if (res.status === 409) {
    // detail is "audio not ready: job is <status>". There is no status
    // endpoint yet, so the status is read out of the message.
    throw new NotReady(/job is (\w+)/.exec(detail)?.[1] ?? "unknown");
  }
  if (res.status === 404) throw new ClientError("The API has no record of this job.");
  throw new ClientError(`The API couldn't fetch the audio (${res.status}): ${detail}`);
}
