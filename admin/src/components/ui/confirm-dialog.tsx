"use client";

import { Button } from "@/components/ui/button";
import {
  DialogClose,
  DialogContent,
  DialogRoot,
  DialogTrigger,
} from "@/components/ui/dialog";
import type { ReactNode } from "react";

export function ConfirmDialog({
  trigger,
  title,
  description,
  confirmLabel = "Confirm",
  confirmVariant = "danger",
  cancelLabel = "Cancel",
  onConfirm,
  open,
  onOpenChange,
  disabled,
}: {
  trigger?: ReactNode;
  title: string;
  description?: string;
  confirmLabel?: string;
  confirmVariant?: "danger" | "primary" | "secondary";
  cancelLabel?: string;
  onConfirm: () => void;
  open?: boolean;
  onOpenChange?: (open: boolean) => void;
  disabled?: boolean;
}) {
  return (
    <DialogRoot open={open} onOpenChange={onOpenChange}>
      {trigger ? <DialogTrigger asChild>{trigger}</DialogTrigger> : null}
      <DialogContent
        title={title}
        footer={
          <>
            <DialogClose asChild>
              <Button variant="secondary">{cancelLabel}</Button>
            </DialogClose>
            <Button
              variant={confirmVariant}
              disabled={disabled}
              onClick={() => {
                onConfirm();
                onOpenChange?.(false);
              }}
            >
              {confirmLabel}
            </Button>
          </>
        }
      >
        {description ? (
          <p className="text-[13px] text-muted">{description}</p>
        ) : null}
      </DialogContent>
    </DialogRoot>
  );
}
