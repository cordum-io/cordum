import { useNavigate, useParams } from "react-router-dom";
import PackDetail from "@/components/packs/PackDetail";

export default function PackDetailPage() {
  const navigate = useNavigate();
  const { id } = useParams<{ id: string }>();

  if (!id) {
    return null;
  }

  return <PackDetail packId={id} onClose={() => navigate("/packs")} />;
}
