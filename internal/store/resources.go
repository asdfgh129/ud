package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/asdfgh129/ud/internal/core"
	"github.com/asdfgh129/ud/internal/security"
)

// SaveResource persists the full P0-frozen Resource graph in one transaction.
func (s *Store) SaveResource(ctx context.Context, taskID core.TaskID, r *core.Resource) error {
	return s.withTx(ctx, func(tx *sql.Tx) error {
		options, err := json.Marshal(struct {
			Limits      core.TaskLimits    `json:"limits"`
			PostProcess []core.ProcessSpec `json:"post_process"`
		}{r.Limits, r.PostProcess})
		if err != nil {
			return err
		}
		// Resource.Headers is runtime-only: the Scheduler injects credential
		// headers from the keychain/CredentialStore right before each request.
		// Persist only non-sensitive headers (docs/06 §6).
		headers, err := json.Marshal(security.StripSensitiveHeaders(r.Headers))
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO resources(id,task_id,kind,engine,options,headers,cred_ref)
			VALUES(?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET kind=excluded.kind,
			engine=excluded.engine, options=excluded.options, headers=excluded.headers, cred_ref=excluded.cred_ref`,
			r.ID, string(taskID), string(r.Kind), string(r.Engine), string(options), string(headers),
			nullable(string(r.Credential))); err != nil {
			return err
		}
		for _, f := range r.Files {
			selected := 0
			if f.Selected {
				selected = 1
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO files(resource_id,idx,rel_path,size,selected)
				VALUES(?,?,?,?,?) ON CONFLICT(resource_id,idx) DO UPDATE SET
				size=excluded.size, selected=excluded.selected, rel_path=excluded.rel_path`,
				r.ID, f.Index, f.Path, f.Size, selected); err != nil {
				return err
			}
		}
		for _, m := range r.Mirrors {
			if _, err := tx.ExecContext(ctx, `INSERT INTO mirrors(resource_id,url,priority,geo,weight,failures)
				VALUES(?,?,?,?,?,?)`, r.ID, m.URL, m.Priority, nullable(m.Geo), m.Weight, m.Failures); err != nil {
				return err
			}
		}
		trackers, err := json.Marshal(r.Identity.Trackers)
		if err != nil {
			return err
		}
		private, adaptive := 0, 0
		if r.Identity.Private {
			private = 1
		}
		if r.Identity.IsAdaptive {
			adaptive = 1
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO protocol_identity
			(resource_id,infohash_v1,btmh_v2,private_flag,trackers_json,passkey_ref,peer_id,
			 ed2k_hash,aich_root,user_hash,ident_key_ref,credits_ref,variant_id,is_adaptive)
			VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(resource_id) DO UPDATE SET
			infohash_v1=excluded.infohash_v1, btmh_v2=excluded.btmh_v2, private_flag=excluded.private_flag,
			trackers_json=excluded.trackers_json, passkey_ref=excluded.passkey_ref, peer_id=excluded.peer_id,
			ed2k_hash=excluded.ed2k_hash, aich_root=excluded.aich_root, user_hash=excluded.user_hash,
			ident_key_ref=excluded.ident_key_ref, credits_ref=excluded.credits_ref,
			variant_id=excluded.variant_id, is_adaptive=excluded.is_adaptive`,
			r.ID, nullable(r.Identity.InfoHashV1), nullable(r.Identity.InfoHashV2), private,
			string(trackers), nullable(r.Identity.PasskeyRef), nullable(r.Identity.PeerID),
			nullable(r.Identity.ED2KFileHash), nullable(r.Identity.AICHRoot), nullable(r.Identity.UserHash),
			nullable(r.Identity.IdentKeyRef), nullable(r.Identity.CreditsRef), nullable(r.Identity.VariantID),
			adaptive); err != nil {
			return err
		}
		if r.Checksum != nil {
			if r.Checksum.Whole != "" {
				if _, err := tx.ExecContext(ctx, `INSERT INTO checksums(resource_id,file_idx,algo,scope,chunk_index,value)
					VALUES(?,0,?,'whole',NULL,?)`, r.ID, r.Checksum.Algo, r.Checksum.Whole); err != nil {
					return err
				}
			}
			for i, piece := range r.Checksum.Pieces {
				if _, err := tx.ExecContext(ctx, `INSERT INTO checksums(resource_id,file_idx,algo,scope,chunk_index,value)
					VALUES(?,0,?,'piece',?,?)`, r.ID, r.Checksum.Algo, i, piece); err != nil {
					return err
				}
			}
		}
		_, err = tx.ExecContext(ctx, `UPDATE tasks SET resource_id=?, updated_at=? WHERE id=?`,
			r.ID, s.now(), string(taskID))
		return err
	})
}

// GetResource rebuilds a Resource from the normalized tables. The JSON round
// trip is covered by T-FR-RSV-06-01 in docs/12.
func (s *Store) GetResource(ctx context.Context, id string) (*core.Resource, error) {
	r := &core.Resource{ID: id}
	var options, headers string
	var cred sql.NullString
	err := s.db.QueryRowContext(ctx, `SELECT kind,engine,options,headers,COALESCE(cred_ref,'')
		FROM resources WHERE id=?`, id).Scan(&r.Kind, &r.Engine, &options, &headers, &cred)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("resource %s not found", id)
	}
	if err != nil {
		return nil, err
	}
	r.Credential = core.CredentialRef(cred.String)
	var envelope struct {
		Limits      core.TaskLimits    `json:"limits"`
		PostProcess []core.ProcessSpec `json:"post_process"`
	}
	if err := json.Unmarshal([]byte(options), &envelope); err != nil {
		return nil, err
	}
	r.Limits, r.PostProcess = envelope.Limits, envelope.PostProcess
	if err := json.Unmarshal([]byte(headers), &r.Headers); err != nil {
		return nil, err
	}

	files, err := s.db.QueryContext(ctx, `SELECT idx,rel_path,size,COALESCE(selected,1),COALESCE(final_path,'')
		FROM files WHERE resource_id=? ORDER BY idx`, id)
	if err != nil {
		return nil, err
	}
	for files.Next() {
		var f core.ResourceFile
		var selected int
		var final string
		if err := files.Scan(&f.Index, &f.Path, &f.Size, &selected, &final); err != nil {
			files.Close()
			return nil, err
		}
		f.Selected = selected != 0
		r.Files = append(r.Files, f)
	}
	files.Close()

	mirrors, err := s.db.QueryContext(ctx, `SELECT url,priority,COALESCE(geo,''),weight,failures
		FROM mirrors WHERE resource_id=? ORDER BY priority,id`, id)
	if err != nil {
		return nil, err
	}
	for mirrors.Next() {
		var m core.Mirror
		if err := mirrors.Scan(&m.URL, &m.Priority, &m.Geo, &m.Weight, &m.Failures); err != nil {
			mirrors.Close()
			return nil, err
		}
		r.Mirrors = append(r.Mirrors, m)
	}
	mirrors.Close()

	var trackers string
	var private, adaptive int
	var v1, v2, passkey, peerID, ed2kHash, aich, userHash, identRef, creditsRef, variant sql.NullString
	err = s.db.QueryRowContext(ctx, `SELECT COALESCE(infohash_v1,''),COALESCE(btmh_v2,''),private_flag,
		COALESCE(trackers_json,''),COALESCE(passkey_ref,''),COALESCE(peer_id,''),
		COALESCE(ed2k_hash,''),COALESCE(aich_root,''),COALESCE(user_hash,''),
		COALESCE(ident_key_ref,''),COALESCE(credits_ref,''),COALESCE(variant_id,''),is_adaptive
		FROM protocol_identity WHERE resource_id=?`, id).
		Scan(&v1, &v2, &private, &trackers, &passkey, &peerID,
			&ed2kHash, &aich, &userHash, &identRef, &creditsRef, &variant, &adaptive)
	if err == nil {
		r.Identity = core.ProtocolIdentity{
			InfoHashV1: v1.String, InfoHashV2: v2.String, Private: private != 0,
			PasskeyRef: passkey.String, PeerID: peerID.String,
			ED2KFileHash: ed2kHash.String, AICHRoot: aich.String, UserHash: userHash.String,
			IdentKeyRef: identRef.String, CreditsRef: creditsRef.String,
			VariantID: variant.String, IsAdaptive: adaptive != 0,
		}
		if trackers != "" && trackers != "null" {
			_ = json.Unmarshal([]byte(trackers), &r.Identity.Trackers)
		}
	} else if err != sql.ErrNoRows {
		return nil, err
	}

	rows, err := s.db.QueryContext(ctx, `SELECT algo,scope,COALESCE(chunk_index,-1),value
		FROM checksums WHERE resource_id=? ORDER BY chunk_index`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var pieces []struct {
		idx   int
		value string
	}
	for rows.Next() {
		var algo, scope, value string
		var idx int
		if err := rows.Scan(&algo, &scope, &idx, &value); err != nil {
			return nil, err
		}
		if r.Checksum == nil {
			r.Checksum = &core.Checksum{Algo: algo}
		}
		if scope == "whole" {
			r.Checksum.Whole = value
		} else {
			pieces = append(pieces, struct {
				idx   int
				value string
			}{idx, value})
		}
	}
	if len(pieces) > 0 {
		sort.Slice(pieces, func(i, j int) bool { return pieces[i].idx < pieces[j].idx })
		for _, p := range pieces {
			r.Checksum.Pieces = append(r.Checksum.Pieces, p.value)
		}
	}
	return r, nil
}

// UpdateFileSizes writes the sizes learned by Transport.Probe before chunk allocation.
func (s *Store) UpdateFileSizes(ctx context.Context, resourceID string, sizes map[int]int64) error {
	return s.withTx(ctx, func(tx *sql.Tx) error {
		for idx, size := range sizes {
			if _, err := tx.ExecContext(ctx, `UPDATE files SET size=? WHERE resource_id=? AND idx=?`,
				size, resourceID, idx); err != nil {
				return err
			}
		}
		return nil
	})
}
