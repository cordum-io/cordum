export default function JsonViewer({ value }: { value: unknown }) {
  return (
    <pre className="overflow-auto rounded-xl border border-white/10 bg-black/30 p-3 text-xs text-zinc-200">
      {JSON.stringify(value, null, 2)}
    </pre>
  );
}

