import { useMemo, useState } from "react";
import { useForm } from "react-hook-form";
import { zodResolver } from "@hookform/resolvers/zod";
import { z } from "zod";
import { ApiError } from "../../api/client";
import { useChangePassword } from "../../hooks/useSettings";
import { cn } from "../../lib/utils";
import { useToastStore } from "../../state/toast";
import { Button } from "../ui/Button";
import { Card } from "../ui/Card";
import { Input } from "../ui/Input";

const passwordSchema = z
  .string()
  .min(12, "Password must be at least 12 characters")
  .regex(/[a-z]/, "Must include a lowercase letter")
  .regex(/[A-Z]/, "Must include an uppercase letter")
  .regex(/[0-9]/, "Must include a number")
  .regex(/[^a-zA-Z0-9]/, "Must include a special character");

const changePasswordSchema = z
  .object({
    current_password: z.string().min(1, "Current password is required"),
    new_password: passwordSchema,
    confirm_password: z.string().min(1, "Please confirm your new password"),
  })
  .refine((input) => input.new_password === input.confirm_password, {
    path: ["confirm_password"],
    message: "Passwords do not match",
  });

type ChangePasswordFormValues = z.infer<typeof changePasswordSchema>;

export interface PasswordStrength {
  label: "Weak" | "Fair" | "Strong" | "Very Strong";
  progressPercent: number;
  barClassName: string;
  labelClassName: string;
}

export const PASSWORD_REQUIREMENTS_TEXT =
  "Use at least 12 characters with uppercase, lowercase, number, and symbol.";

function passwordScore(password: string): number {
  let score = 0;
  if (password.length >= 12) score += 1;
  if (/[a-z]/.test(password)) score += 1;
  if (/[A-Z]/.test(password)) score += 1;
  if (/[0-9]/.test(password)) score += 1;
  if (/[^a-zA-Z0-9]/.test(password)) score += 1;
  return score;
}

export function evaluatePasswordStrength(password: string): PasswordStrength {
  const score = passwordScore(password);
  if (score <= 1) {
    return {
      label: "Weak",
      progressPercent: 25,
      barClassName: "bg-destructive",
      labelClassName: "text-destructive",
    };
  }
  if (score === 2) {
    return {
      label: "Fair",
      progressPercent: 50,
      barClassName: "bg-[var(--color-warning)]",
      labelClassName: "text-[var(--color-warning)]",
    };
  }
  if (score <= 4) {
    return {
      label: "Strong",
      progressPercent: 75,
      barClassName: "bg-[var(--color-warning)]",
      labelClassName: "text-[var(--color-warning)]",
    };
  }
  return {
    label: "Very Strong",
    progressPercent: 100,
    barClassName: "bg-[var(--color-success)]",
    labelClassName: "text-[var(--color-success)]",
  };
}

export function PasswordStrengthIndicator({
  password,
}: {
  password: string;
}) {
  const strength = useMemo(
    () => evaluatePasswordStrength(password),
    [password],
  );

  return (
    <div className="space-y-1">
      <div className="h-2 w-full rounded-full bg-surface2">
        <div
          className={cn(
            "h-2 rounded-full transition-all duration-200",
            strength.barClassName,
          )}
          style={{ width: `${strength.progressPercent}%` }}
        />
      </div>
      <p className="text-xs text-muted-foreground">
        Password strength:{" "}
        <span className={cn("font-semibold", strength.labelClassName)}>
          {strength.label}
        </span>
      </p>
    </div>
  );
}

export function ChangePasswordSection() {
  const changePassword = useChangePassword();
  const [errorMessage, setErrorMessage] = useState<string | null>(null);

  const {
    register,
    handleSubmit,
    watch,
    reset,
    formState: { errors },
  } = useForm<ChangePasswordFormValues>({
    resolver: zodResolver(changePasswordSchema),
    defaultValues: {
      current_password: "",
      new_password: "",
      confirm_password: "",
    },
  });

  const newPassword = watch("new_password");

  async function onSubmit(values: ChangePasswordFormValues) {
    setErrorMessage(null);
    try {
      await changePassword.mutateAsync({
        current_password: values.current_password,
        new_password: values.new_password,
      });
      useToastStore.getState().addToast({
        type: "success",
        title: "Password updated",
      });
      reset();
    } catch (error) {
      if (
        error instanceof ApiError &&
        (error.status === 401 ||
          /current password/i.test(error.message) ||
          /invalid current password/i.test(error.message))
      ) {
        setErrorMessage("Incorrect current password");
        return;
      }
      setErrorMessage(
        error instanceof Error ? error.message : "Failed to update password",
      );
    }
  }

  return (
    <Card>
      <div className="space-y-4">
        <div>
          <h2 className="font-display text-lg font-semibold text-ink">
            Change Your Password
          </h2>
          <p className="mt-1 text-xs text-muted-foreground">{PASSWORD_REQUIREMENTS_TEXT}</p>
        </div>

        {errorMessage && (
          <div className="rounded-xl bg-[color:rgba(184,58,58,0.14)] px-4 py-2 text-sm text-danger">
            {errorMessage}
          </div>
        )}

        <form onSubmit={handleSubmit(onSubmit)} className="space-y-4">
          <div>
            <label className="mb-1 block text-xs font-semibold text-muted-foreground">
              Current Password
            </label>
            <Input
              type="password"
              autoComplete="current-password"
              placeholder="Enter current password"
              {...register("current_password")}
            />
            {errors.current_password && (
              <p className="mt-1 text-xs text-danger">
                {errors.current_password.message}
              </p>
            )}
          </div>

          <div>
            <label className="mb-1 block text-xs font-semibold text-muted-foreground">
              New Password
            </label>
            <Input
              type="password"
              autoComplete="new-password"
              placeholder="Enter new password"
              {...register("new_password")}
            />
            <div className="mt-2">
              <PasswordStrengthIndicator password={newPassword ?? ""} />
            </div>
            {errors.new_password && (
              <p className="mt-1 text-xs text-danger">
                {errors.new_password.message}
              </p>
            )}
          </div>

          <div>
            <label className="mb-1 block text-xs font-semibold text-muted-foreground">
              Confirm New Password
            </label>
            <Input
              type="password"
              autoComplete="new-password"
              placeholder="Confirm new password"
              {...register("confirm_password")}
            />
            {errors.confirm_password && (
              <p className="mt-1 text-xs text-danger">
                {errors.confirm_password.message}
              </p>
            )}
          </div>

          <div className="flex justify-end">
            <Button type="submit" size="sm" disabled={changePassword.isPending}>
              {changePassword.isPending ? "Updating..." : "Update Password"}
            </Button>
          </div>
        </form>
      </div>
    </Card>
  );
}
