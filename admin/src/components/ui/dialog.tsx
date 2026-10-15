"use client";

import * as Dialog from "@radix-ui/react-dialog";
import { IconClose } from "@/components/icons";
import { cn } from "@/lib/utils";

export const DialogRoot = Dialog.Root;
export const DialogTrigger = Dialog.Trigger;
export const DialogClose = Dialog.Close;

export function DialogContent({
  title,
  description,
  footer,
  children,
  className,
}: {
  title: string;
  description?: string;
  footer?: React.ReactNode;
  children: React.ReactNode;
  className?: string;
}) {
  return (
    <Dialog.Portal>
      <Dialog.Overlay className="fixed inset-0 z-50 bg-overlay" />
      <Dialog.Content
        className={cn(
          "fixed left-1/2 top-[8vh] z-50 w-[min(560px,calc(100vw-32px))] -translate-x-1/2 rounded-panel border border-border bg-surface shadow-[var(--shadow-dialog)]",
          className,
        )}
      >
        <header className="flex items-start justify-between gap-4 border-b border-border-subtle px-4 py-3">
          <div>
            <Dialog.Title className="text-[15px] font-semibold text-ink">
              {title}
            </Dialog.Title>
            {description ? (
              <Dialog.Description className="mt-0.5 text-[12px] text-muted">
                {description}
              </Dialog.Description>
            ) : null}
          </div>
          <Dialog.Close
            aria-label="Close dialog"
            className="rounded-[3px] p-1 text-muted transition-colors hover:bg-surface-muted hover:text-ink"
          >
            <IconClose size={16} />
          </Dialog.Close>
        </header>
        <div className="max-h-[64vh] overflow-y-auto px-4 py-4">{children}</div>
        {footer ? (
          <footer className="flex items-center justify-end gap-2 border-t border-border-subtle px-4 py-3">
            {footer}
          </footer>
        ) : null}
      </Dialog.Content>
    </Dialog.Portal>
  );
}
