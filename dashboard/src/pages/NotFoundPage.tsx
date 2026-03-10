/*
 * DESIGN: "Control Surface" — 404
 * Matches cordumds-gj5mw4zm.manus.space showcase patterns
 */
import { useNavigate } from "react-router-dom";
import { motion } from "framer-motion";
import { Button } from "@/components/ui/Button";
import { Home, ArrowLeft } from "lucide-react";

export default function NotFoundPage() {
  const navigate = useNavigate();
  return (
    <div className="flex flex-col items-center justify-center min-h-[60vh] relative">
      <div className="absolute top-1/2 left-1/2 -translate-x-1/2 -translate-y-1/2 w-[300px] h-[300px] rounded-full bg-cordum/5 blur-[80px] pointer-events-none" />
      <motion.div
        initial={{ opacity: 0, y: 20 }}
        animate={{ opacity: 1, y: 0 }}
        transition={{ duration: 0.5 }}
        className="text-center relative z-10"
      >
        <span className="font-mono text-7xl font-bold text-cordum/20">404</span>
        <h1 className="font-display font-bold text-xl text-foreground mt-4">Page Not Found</h1>
        <p className="text-sm text-muted-foreground mt-2 max-w-sm mx-auto">
          The resource you're looking for doesn't exist or has been moved.
        </p>
        <div className="flex gap-3 justify-center mt-6">
          <Button variant="outline" size="sm" onClick={() => navigate(-1)}>
            <ArrowLeft className="w-3 h-3 mr-1" />
            Go Back
          </Button>
          <Button variant="primary" size="sm" onClick={() => navigate("/")}>
            <Home className="w-3 h-3 mr-1" />
            Dashboard
          </Button>
        </div>
      </motion.div>
    </div>
  );
}
