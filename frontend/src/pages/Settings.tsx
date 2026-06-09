import { useEffect, useState } from 'react';

type DPIMode = 'none' | 'suricata' | 'tzsp';

const MODE_OPTIONS: { value: DPIMode; label: string; description: string }[] = [
  {
    value: 'none',
    label: 'Desativado',
    description: 'Os fluxos não são identificados por aplicação (campo "Application" fica vazio).',
  },
  {
    value: 'suricata',
    label: 'Correlação com Suricata',
    description: 'Reaproveita os eventos TLS/DNS/HTTP do eve.json do Suricata, correlacionando por 5-tupla.',
  },
  {
    value: 'tzsp',
    label: 'Captura TZSP',
    description: 'Recebe cópias de pacotes via TZSP (porta 37008/udp) e extrai SNI/DNS diretamente.',
  },
];

export default function Settings() {
  const [mode, setMode] = useState<DPIMode | null>(null);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState('');
  const [savedMessage, setSavedMessage] = useState('');

  useEffect(() => {
    fetch('/api/settings')
      .then((res) => {
        if (!res.ok) throw new Error('Falha ao carregar configurações');
        return res.json();
      })
      .then((data: { dpi_mode: DPIMode }) => setMode(data.dpi_mode))
      .catch(() => setError('Não foi possível carregar as configurações.'));
  }, []);

  const handleSave = async (newMode: DPIMode) => {
    setSaving(true);
    setError('');
    setSavedMessage('');
    try {
      const res = await fetch('/api/settings', {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ dpi_mode: newMode }),
      });
      if (!res.ok) throw new Error('Falha ao salvar');
      setMode(newMode);
      setSavedMessage('Configuração salva.');
    } catch {
      setError('Não foi possível salvar a configuração.');
    } finally {
      setSaving(false);
    }
  };

  return (
    <div className="p-6 max-w-2xl">
      <h1 className="text-2xl font-bold mb-4">Configurações</h1>
      <div className="bg-gray-900 border border-gray-800 p-4 rounded-xl shadow-lg">
        <h2 className="text-gray-400 text-sm font-semibold mb-1">
          Identificação de Aplicações (DPI)
        </h2>
        <p className="text-gray-500 text-xs mb-4">
          Escolha como o Flux.io identifica a aplicação por trás de cada fluxo de rede.
        </p>

        {error && <div className="bg-red-900/50 text-red-200 p-2 mb-3 rounded text-sm">{error}</div>}
        {savedMessage && <div className="bg-green-900/50 text-green-200 p-2 mb-3 rounded text-sm">{savedMessage}</div>}

        <div className="space-y-3">
          {MODE_OPTIONS.map((opt) => (
            <label
              key={opt.value}
              className="flex items-start space-x-3 p-3 bg-gray-950 border border-gray-800 rounded-lg cursor-pointer hover:border-blue-600 transition-colors"
            >
              <input
                type="radio"
                name="dpi_mode"
                className="mt-1"
                checked={mode === opt.value}
                disabled={saving || mode === null}
                onChange={() => handleSave(opt.value)}
              />
              <div>
                <div className="text-white font-medium">{opt.label}</div>
                <div className="text-gray-500 text-xs">{opt.description}</div>
              </div>
            </label>
          ))}
        </div>
      </div>
    </div>
  );
}
