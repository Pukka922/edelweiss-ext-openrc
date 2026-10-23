import { createUniqueId, type JSX, onCleanup, onMount, Show } from "solid-js";
import type { Failure } from "./useJob";

/**
 * The close of every open modal, the topmost (the one opened last) at the end: Escape goes to it alone, so a dialog
 * above another one closes without the one below, and a busy one closes nothing.
 */
const stack: (() => void)[] = [];

const onEscape = (e: KeyboardEvent) => {
  if (e.key === "Escape") stack[stack.length - 1]?.();
};

/**
 * A modal dialog like the SDK's ConfirmDialog, with content of its own. Escape (when it is the topmost) and the
 * backdrop close it unless busy.
 */
export function Modal(props: {
  title: string;
  busy?: boolean;
  wide?: boolean;
  onClose: () => void;
  children: JSX.Element;
  actions: JSX.Element;
}) {
  const close = () => {
    if (!props.busy) props.onClose();
  };
  onMount(() => {
    if (stack.push(close) === 1) document.addEventListener("keydown", onEscape);
  });
  onCleanup(() => {
    const i = stack.lastIndexOf(close);
    if (i >= 0) stack.splice(i, 1);
    if (stack.length === 0) document.removeEventListener("keydown", onEscape);
  });
  return (
    <div class="modal modal-open" role="dialog" aria-modal="true" aria-label={props.title}>
      <div class={props.wide ? "modal-box max-w-3xl" : "modal-box"}>
        <h3 class="text-lg font-bold">{props.title}</h3>
        <div class="flex flex-col gap-3 py-4">{props.children}</div>
        <div class="modal-action">{props.actions}</div>
      </div>
      <div class="modal-backdrop" aria-hidden="true" onClick={close} />
    </div>
  );
}

/** A labelled control: children get the id that the label points to. */
export function Field(props: { label: string; children: (id: string) => JSX.Element }) {
  const id = createUniqueId();
  return (
    <div class="flex flex-col gap-1">
      <label class="text-sm" for={id}>
        {props.label}
      </label>
      {props.children(id)}
    </div>
  );
}

/** A checkbox with its text. */
export function Check(props: {
  label: JSX.Element;
  checked: boolean;
  disabled?: boolean;
  onChange: (on: boolean) => void;
}) {
  return (
    <label class="label cursor-pointer justify-start gap-2 whitespace-normal">
      <input
        type="checkbox"
        class="checkbox checkbox-sm"
        checked={props.checked}
        disabled={props.disabled}
        onChange={(e) => props.onChange(e.currentTarget.checked)}
      />
      <span>{props.label}</span>
    </label>
  );
}

/**
 * The job of a dialog: the step that runs, or the text of its failure with the output of the failed command in a pre; a
 * job whose state is not known (a neutral failure) only says so, as a status.
 */
export function JobStatus(props: { step?: string; failure?: Failure }) {
  return (
    <>
      <Show when={props.step}>
        {(step) => (
          <p class="flex items-center gap-2 text-sm">
            <span class="loading loading-spinner loading-sm" aria-hidden="true" />
            {step()}
          </p>
        )}
      </Show>
      <Show when={props.failure?.neutral ? props.failure : undefined}>
        {(f) => (
          <div class="alert alert-info text-sm" role="status">
            {f().text}
          </div>
        )}
      </Show>
      <Show when={props.failure?.neutral ? undefined : props.failure}>
        {(f) => (
          <div class="alert alert-error flex-col items-start text-sm" role="alert">
            <span>{f().text}</span>
            <Show when={f().output}>
              {(output) => (
                <pre class="max-h-48 w-full overflow-auto whitespace-pre-wrap rounded-box bg-base-200 p-2 text-xs text-base-content">
                  {output()}
                </pre>
              )}
            </Show>
          </div>
        )}
      </Show>
    </>
  );
}
