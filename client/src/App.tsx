import type { ReactNode } from "react";
import "./App.css";
import { AudioPlayer } from "./components/AudioPlayer";
import { Dropzone } from "./components/Dropzone";
import { Step, type StepState } from "./components/Step";
import { formatBytes } from "./format";
import { useAudioJob, type Phase } from "./useAudioJob";

/** Which of the three steps (0 upload, 1 extract, 2 listen) the job is on. */
function currentStep(phase: Phase): number {
  switch (phase.step) {
    case "choose":
    case "upload":
      return 0;
    case "extract":
      return 1;
    case "listen":
      return 2;
    case "failed":
      return phase.at === "upload" ? 0 : 1;
  }
}

function stateOf(index: number, phase: Phase): StepState {
  const current = currentStep(phase);
  if (index < current) return "done";
  if (index > current) return "todo";
  return phase.step === "failed" ? "error" : "active";
}

const STATUS_TEXT: Record<string, string> = {
  pending: "Waiting for a worker to pick up the video.",
  processing: "Extracting the audio track.",
  failed: "The last attempt failed. Trying again.",
};

export default function App() {
  const { phase, start, checkAgain, refreshAudio, reset } = useAudioJob();
  const file = phase.step === "choose" ? undefined : phase.file;

  return (
    <div className="page">
      <header className="masthead">
        <span className="wordmark">
          <WaveMark />
          Swift Stream
        </span>
      </header>

      <main>
        <h1>Pull the soundtrack out of any video</h1>
        <p className="lede">
          Upload a clip and the pipeline extracts its audio as an MP3. Play it here or save it to your computer.
        </p>

        <ol className="steps">
          <Step
            number={1}
            title="Upload the video"
            state={stateOf(0, phase)}
            fill={phase.step === "upload" ? phase.loaded / phase.total : undefined}
          >
            {phase.step === "choose" && <Dropzone onFile={start} />}

            {phase.step === "upload" && (
              <div className="upload">
                <p className="file-name">{phase.file.name}</p>
                <Progress loaded={phase.loaded} total={phase.total} />
                <button type="button" className="button button-quiet" onClick={reset}>
                  Cancel upload
                </button>
              </div>
            )}

            {phase.step === "failed" && phase.at === "upload" && (
              <Problem message={phase.message}>
                {phase.retryable && (
                  <button type="button" className="button" onClick={() => start(phase.file)}>
                    Try again
                  </button>
                )}
                <button type="button" className="button button-quiet" onClick={reset}>
                  Choose a different video
                </button>
              </Problem>
            )}

            {stateOf(0, phase) === "done" && file && (
              <p className="note">
                {file.name} <span className="muted">({formatBytes(file.size)})</span>
              </p>
            )}
          </Step>

          <Step number={2} title="Extract the audio" state={stateOf(1, phase)} busy={phase.step === "extract"}>
            {stateOf(1, phase) === "todo" && <p className="muted">Starts as soon as the upload finishes.</p>}

            {phase.step === "extract" && (
              <>
                <p className="note" role="status">
                  {STATUS_TEXT[phase.status] ?? `The job is ${phase.status}.`}
                </p>
                <JobId id={phase.jobId} />
              </>
            )}

            {phase.step === "failed" && phase.at === "extract" && (
              <Problem message={phase.message}>
                <button type="button" className="button" onClick={() => checkAgain(phase.file, phase.jobId)}>
                  Check again
                </button>
                <button type="button" className="button button-quiet" onClick={reset}>
                  Choose a different video
                </button>
                <JobId id={phase.jobId} />
              </Problem>
            )}

            {stateOf(1, phase) === "done" && <p className="note">Saved as an MP3.</p>}
          </Step>

          <Step number={3} title="Listen and download" state={stateOf(2, phase)}>
            {phase.step === "listen" ? (
              <>
                <AudioPlayer
                  audio={phase.audio}
                  fileName={phase.file.name}
                  onRefresh={() => refreshAudio(phase.file, phase.jobId)}
                />
                <button type="button" className="button button-quiet" onClick={reset}>
                  Extract another video
                </button>
              </>
            ) : (
              <p className="muted">The MP3 shows up here, ready to play or save.</p>
            )}
          </Step>
        </ol>
      </main>
    </div>
  );
}

function Progress({ loaded, total }: { loaded: number; total: number }) {
  const percent = Math.round((loaded / total) * 100);
  return (
    <>
      <div
        className="bar"
        role="progressbar"
        aria-label="Upload progress"
        aria-valuemin={0}
        aria-valuemax={100}
        aria-valuenow={percent}
      >
        <div className="bar-fill" style={{ transform: `scaleX(${loaded / total})` }} />
      </div>
      <p className="progress-text">
        <strong>{percent}%</strong>
        <span className="muted">
          {formatBytes(loaded)} of {formatBytes(total)}
        </span>
      </p>
    </>
  );
}

function Problem({ message, children }: { message: string; children: ReactNode }) {
  return (
    <div className="problem">
      <p role="alert">{message}</p>
      <div className="actions">{children}</div>
    </div>
  );
}

function JobId({ id }: { id: string }) {
  return (
    <p className="job-id muted">
      Job ID <code>{id}</code>
    </p>
  );
}

function WaveMark() {
  return (
    <svg viewBox="0 0 24 24" width="22" height="22" aria-hidden="true">
      <g stroke="currentColor" strokeWidth="2.5" strokeLinecap="round">
        <path d="M4 10v4M9 6v12M14 3v18M19 8v8" />
      </g>
    </svg>
  );
}
