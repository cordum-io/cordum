import { Loader2 } from "lucide-react";

export function LoadingScreen() {
  return (
    <div className="flex items-center justify-center min-h-screen bg-background">
      <div className="flex flex-col items-center gap-4">
        <div className="relative">
          <div className="w-10 h-10 rounded-full border-2 border-cordum/20" />
          <Loader2 className="absolute inset-0 w-10 h-10 text-cordum animate-spin" />
        </div>
        <p className="text-sm text-muted-foreground font-mono tracking-wider uppercase">
          Loading…
        </p>
      </div>
    </div>
  );
}
